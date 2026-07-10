# Polypus MLX backend

Python **uv** project for **mlx-audio** on Apple Silicon. Polypus gateway (`polypus serve`) proxies public `:1320` to this backend on `:1322`.

```bash
make -C polypus mlx-sync
make -C polypus mlx-serve    # backend only (debug)
make -C polypus serve        # gateway + backend
```

See `scripts/serve_launcher.py` for mlx-audio 0.4.4 compatibility patches.
