"""Start mlx_audio.server with Qwen3 continuous batching disabled.

mlx-audio 0.4.4 routes Qwen3 through create_tts_batch_session, which calls
finished.tolist() from the inference broker thread without mx.eval(). That
raises RuntimeError: There is no Stream(gpu, 0) in current thread.

Serial model.generate() works; disable continuous batch until upstream fixes.
"""

from __future__ import annotations


def _disable_qwen3_server_batching() -> None:
    """Force mlx_audio.server TTSExecutionAdapter.run_serial (model.generate).

    Qwen3 batch_generate / continuous batch emits ~2s speech then pads to ~96s
    silence for longer inputs. Serial generate splits on newlines and works.
    """
    from mlx_audio.tts.models.qwen3_tts.qwen3_tts import Model

    def _no_server_batch(self, **kwargs) -> bool:  # noqa: ANN001, ARG001
        return False

    Model.supports_tts_continuous_batch = _no_server_batch  # type: ignore[method-assign]
    Model.supports_tts_batch = _no_server_batch  # type: ignore[method-assign]


def _patch_inference_broker_stream() -> None:
    """MLX 0.32+ streams are thread-local; mlx_audio.server runs GPU work on a broker thread."""
    import mlx.core as mx
    from mlx_audio.server_inference import InferenceBroker

    original_run = InferenceBroker._run

    def _run_with_stream(self) -> None:  # noqa: ANN001
        mx.new_thread_local_stream(mx.default_device())
        original_run(self)

    InferenceBroker._run = _run_with_stream  # type: ignore[method-assign]


def _patch_preflight_model_load() -> None:
    """Skip asyncio.to_thread preflight; it loads weights on a pool thread, not the broker."""
    import mlx_audio.server as server

    async def _preflight_noop(model_name: str) -> None:  # noqa: ARG001
        return None

    server._preflight_model_load = _preflight_noop


def main() -> None:
    _disable_qwen3_server_batching()
    _patch_inference_broker_stream()
    import mlx_audio.server  # noqa: F401 — register routes before preflight patch

    _patch_preflight_model_load()
    from mlx_audio.server import main as server_main

    server_main()


if __name__ == "__main__":
    main()
