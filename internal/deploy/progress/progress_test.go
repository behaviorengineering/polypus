package progress

import (
	"errors"
	"strings"
	"testing"
)

type failWriter struct{ n int }

func (f *failWriter) Write(p []byte) (int, error) {
	f.n++
	return 0, errors.New("closed")
}

func TestPrinterWrites(t *testing.T) {
	var b strings.Builder
	p := New(&b)
	p.Printf("a %d\n", 1)
	p.Println("b")
	if b.String() != "a 1\nb\n" || p.Err() != nil {
		t.Fatalf("got %q err %v", b.String(), p.Err())
	}
}

func TestPrinterKeepsFirstErrorAndStops(t *testing.T) {
	w := &failWriter{}
	p := New(w)
	p.Println("x")
	p.Printf("y\n")
	if p.Err() == nil || w.n != 1 {
		t.Fatalf("err %v writes %d, want the first error kept and no more writes", p.Err(), w.n)
	}
}
