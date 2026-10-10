# Polypus MLX backend

Python **uv** project for **mlx-audio** on Apple Silicon. Polypus gateway (`polypus serve`) proxies public `:1320` to this backend on `:1322`.

```bash
go tool task mlx-sync
go tool task serve        # process-compose: gateway + MLX :1322
```

See `scripts/serve_launcher.py` for mlx-audio 0.4.4 compatibility patches.
