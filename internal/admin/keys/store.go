package keys

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/statefile"
)

const maxKeys = 32

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// Config creates a Store.
type Config struct {
	Path  string
	Clock func() time.Time
	Rand  io.Reader
}

// Store manages hashed gateway access keys on disk.
type Store struct {
	path  string
	clock func() time.Time
	rand  io.Reader

	// mu guards cache, the parsed key file Authorize and Verify read. It is
	// reloaded only when the file on disk is replaced or its mtime or size change.
	mu     sync.Mutex
	cache  *cachedFile
	parses atomic.Int64 // files parsed by read, for tests.
}

// cachedFile is a parsed key file and the file info it was read under.
type cachedFile struct {
	info fs.FileInfo
	file filePayload
}

// Record is one key metadata row (no plaintext).
type Record struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Hash      string `json:"hash"`
	CreatedAt string `json:"created_at"`
	RotatedAt string `json:"rotated_at,omitempty"`
}

type filePayload struct {
	Version int      `json:"version"`
	Keys    []Record `json:"keys"`
}

// tokenPrefix starts every gateway access key: ppk.<id>.<secret>. parseToken must
// match it exactly.
const tokenPrefix = "ppk."

// CreateStore opens the key store at cfg.Path.
func (cfg Config) CreateStore() (*Store, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, derrors.New(derrors.CodeInvalid, "keys.CreateStore", "path required")
	}
	if cfg.Clock == nil {
		return nil, derrors.New(derrors.CodeInvalid, "keys.CreateStore", "clock required")
	}
	r := cfg.Rand
	if r == nil {
		r = rand.Reader
	}
	return &Store{path: cfg.Path, clock: cfg.Clock, rand: r}, nil
}

// GenerateResult holds a one-time plaintext key.
type GenerateResult struct {
	Record Record
	Key    string
}

// Generate adds a new named key.
func (s *Store) Generate(ctx context.Context, name string) (GenerateResult, error) {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return GenerateResult{}, derrors.New(derrors.CodeInvalid, "keys.Generate", "invalid name")
	}
	var out GenerateResult
	err := s.withLock(ctx, "keys.Generate", func() error {
		file, err := s.read()
		if err != nil {
			return err
		}
		if err := canAdd(file, name); err != nil {
			return err
		}
		id, secret, token, err := s.newToken()
		if err != nil {
			return err
		}
		rec := Record{
			ID:        id,
			Name:      name,
			Prefix:    tokenPrefix + id,
			Hash:      hashSecret([]byte(secret)),
			CreatedAt: s.clock().UTC().Format(time.RFC3339),
		}
		file.Keys = append(file.Keys, rec)
		if err := s.write(file); err != nil {
			return err
		}
		out = GenerateResult{Record: rec, Key: token}
		return nil
	})
	return out, err
}

// CheckGenerate reports whether Generate would accept name now, without writing:
// the name is valid, free, and the store is below its key limit.
func (s *Store) CheckGenerate(name string) error {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return derrors.New(derrors.CodeInvalid, "keys.CheckGenerate", "invalid name")
	}
	file, err := s.read()
	if err != nil {
		return err
	}
	return canAdd(file, name)
}

// canAdd checks the key limit and that name is not taken.
func canAdd(file filePayload, name string) error {
	if len(file.Keys) >= maxKeys {
		return derrors.New(derrors.CodeFailedPrecondition, "keys.Generate", "max keys reached")
	}
	for _, k := range file.Keys {
		if k.Name == name {
			return derrors.New(derrors.CodeConflict, "keys.Generate", "name already exists")
		}
	}
	return nil
}

// Rotate replaces the secret for an existing name.
func (s *Store) Rotate(ctx context.Context, name string) (GenerateResult, error) {
	name = strings.TrimSpace(name)
	var out GenerateResult
	err := s.withLock(ctx, "keys.Rotate", func() error {
		file, err := s.read()
		if err != nil {
			return err
		}
		idx := -1
		for i, k := range file.Keys {
			if k.Name == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return derrors.New(derrors.CodeNotFound, "keys.Rotate", "key not found")
		}
		secret, token, err := s.secretForID(file.Keys[idx].ID)
		if err != nil {
			return err
		}
		file.Keys[idx].Hash = hashSecret([]byte(secret))
		file.Keys[idx].RotatedAt = s.clock().UTC().Format(time.RFC3339)
		if err := s.write(file); err != nil {
			return err
		}
		out = GenerateResult{Record: file.Keys[idx], Key: token}
		return nil
	})
	return out, err
}

// Delete removes a key by name.
func (s *Store) Delete(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	return s.withLock(ctx, "keys.Delete", func() error {
		file, err := s.read()
		if err != nil {
			return err
		}
		var kept []Record
		found := false
		for _, k := range file.Keys {
			if k.Name == name {
				found = true
				continue
			}
			kept = append(kept, k)
		}
		if !found {
			return derrors.New(derrors.CodeNotFound, "keys.Delete", "key not found")
		}
		file.Keys = kept
		return s.write(file)
	})
}

// List returns metadata for all keys.
func (s *Store) List() ([]Record, error) {
	file, err := s.read()
	if err != nil {
		return nil, err
	}
	return append([]Record(nil), file.Keys...), nil
}

// Verify checks a bearer token against the store. It reads the cached key file and
// does not upgrade a legacy hash; Authorize does.
func (s *Store) Verify(token string) (Record, error) {
	file, err := s.readCached()
	if err != nil {
		return Record{}, err
	}
	rec, _, err := verifyIn(file, token)
	return rec, err
}

// Authorize decides one request from the cached key file: open is true when no keys
// exist (the gateway is unlocked); otherwise token must verify, and a bad or
// missing token is CodeUnauthorized. A key still stored as argon2id is rewritten as
// SHA-256 under the file lock after it matches; a failed rewrite is returned, so a
// key file the gateway cannot write is reported instead of re-hashed on every call.
func (s *Store) Authorize(ctx context.Context, token string) (open bool, err error) {
	if ctx == nil {
		return false, derrors.New(derrors.CodeInvalid, "keys.Authorize", "context is required")
	}
	file, err := s.readCached()
	if err != nil {
		return false, err
	}
	if len(file.Keys) == 0 {
		return true, nil
	}
	rec, legacy, err := verifyIn(file, token)
	if err != nil || !legacy {
		return false, err
	}
	_, secret, _ := parseToken(token)
	if err := s.upgradeHash(ctx, rec.ID, rec.Hash, []byte(secret)); err != nil {
		return false, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), "keys.Authorize", "upgrade legacy hash").
			With("key_id", rec.ID)
	}
	return false, nil
}

// upgradeHash rewrites key id's argon2id hash as SHA-256 of secret, which has just
// matched it. Under the file lock it rewrites only when the stored hash is still
// legacyHash, so a rotate or delete in between wins.
func (s *Store) upgradeHash(ctx context.Context, id, legacyHash string, secret []byte) error {
	return s.withLock(ctx, "keys.upgradeHash", func() error {
		file, err := s.read()
		if err != nil {
			return err
		}
		for i := range file.Keys {
			if file.Keys[i].ID == id && file.Keys[i].Hash == legacyHash {
				file.Keys[i].Hash = hashSecret(secret)
				return s.write(file)
			}
		}
		return nil
	})
}

// verifyIn checks token against file. legacy is true when the matching record
// still holds an argon2id hash.
func verifyIn(file filePayload, token string) (rec Record, legacy bool, err error) {
	id, secret, ok := parseToken(token)
	if !ok {
		return Record{}, false, derrors.New(derrors.CodeUnauthorized, "keys.Verify", "invalid token")
	}
	for _, k := range file.Keys {
		if k.ID != id {
			continue
		}
		if match, old := verifyHash(k.Hash, []byte(secret)); match {
			return k, old, nil
		}
		break
	}
	return Record{}, false, derrors.New(derrors.CodeUnauthorized, "keys.Verify", "invalid token")
}

// readCached returns the parsed key file, parsing it again only when the file was
// replaced (every write renames a new file into place) or its mtime or size
// changed. A missing file is an empty store and is not cached.
func (s *Store) readCached() (filePayload, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return filePayload{Version: 1}, nil
		}
		return filePayload{}, derrors.Wrap(err, derrors.CodeInternal, "keys.read", "stat")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.cache; c != nil && sameFileState(c.info, info) {
		return c.file, nil
	}
	// The stat came first, so a change after it makes the next call parse again.
	file, err := s.read()
	if err != nil {
		return filePayload{}, err
	}
	s.cache = &cachedFile{info: info, file: file}
	return file, nil
}

// sameFileState reports whether a and b describe the same file with the same
// mtime and size.
func sameFileState(a, b fs.FileInfo) bool {
	return os.SameFile(a, b) && a.ModTime().Equal(b.ModTime()) && a.Size() == b.Size()
}

func (s *Store) read() (filePayload, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return filePayload{Version: 1, Keys: nil}, nil
		}
		return filePayload{}, derrors.Wrap(err, derrors.CodeInternal, "keys.read", "read")
	}
	var file filePayload
	if err := json.Unmarshal(raw, &file); err != nil {
		return filePayload{}, derrors.Wrap(err, derrors.CodeInternal, "keys.read", "parse")
	}
	s.parses.Add(1)
	if file.Version == 0 {
		file.Version = 1
	}
	return file, nil
}

// withLock runs fn holding the key file's lock. fn's own error is returned as it
// is; a lock failure is wrapped with op, the key operation that wanted the lock.
func (s *Store) withLock(ctx context.Context, op string, fn func() error) error {
	var fnErr error
	if err := statefile.WithFileLock(ctx, s.path, func() error {
		fnErr = fn()
		return nil
	}); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), op, "lock key file").With("path", s.path)
	}
	return fnErr
}

func (s *Store) write(file filePayload) error {
	file.Version = 1
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "keys.write", "marshal")
	}
	if err := statefile.WriteAtomic(s.path, raw, 0o600); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), "keys.write", "write").With("path", s.path)
	}
	// Drop the cache so this process never serves the old file, even when the
	// filesystem's mtime is too coarse to tell the two apart.
	s.mu.Lock()
	s.cache = nil
	s.mu.Unlock()
	return nil
}

func (s *Store) newToken() (id string, secret string, token string, err error) {
	idBytes := make([]byte, 16)
	if _, err := io.ReadFull(s.rand, idBytes); err != nil {
		return "", "", "", derrors.Wrap(err, derrors.CodeInternal, "keys.newToken", "id rand")
	}
	id = hex.EncodeToString(idBytes)
	secret, token, err = s.secretForID(id)
	return id, secret, token, err
}

func (s *Store) secretForID(id string) (secret string, token string, err error) {
	secretBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.rand, secretBytes); err != nil {
		return "", "", derrors.Wrap(err, derrors.CodeInternal, "keys.secretForID", "secret rand")
	}
	secret = base64.RawURLEncoding.EncodeToString(secretBytes)
	token = tokenPrefix + id + "." + secret
	return secret, token, nil
}

func parseToken(token string) (id, secret string, ok bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, tokenPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(token, tokenPrefix)
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
