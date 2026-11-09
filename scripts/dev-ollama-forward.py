#!/usr/bin/env python3
"""Development-only TCP forwarder for Ollama on macOS.

The supported runtime is the Core Pod, but Ollama binds loopback only by
default and the Podman VM cannot reach the host's 127.0.0.1. This forwards a
reachable host port to the loopback Ollama so a local benchmark can run:

    python3 scripts/dev-ollama-forward.py --listen 0.0.0.0:11435

Then run the benchmark with:

    BENCH_OLLAMA=http://host.containers.internal:11435 make bench-v3-run ...

It binds a non-loopback address; use it only on a trusted development host and
stop it when the run is over. The production fix is to bind Ollama to an
address the Podman VM can reach (see docs/deploy-core-pod.md).
"""

import argparse
import asyncio


async def pipe(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
    try:
        while True:
            chunk = await reader.read(65536)
            if not chunk:
                break
            writer.write(chunk)
            await writer.drain()
    except (ConnectionResetError, BrokenPipeError):
        pass
    finally:
        try:
            writer.close()
        except Exception:
            pass


async def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listen", default="0.0.0.0:11435", help="listen address")
    parser.add_argument("--upstream", default="127.0.0.1:11434", help="Ollama address")
    args = parser.parse_args()
    host, _, port = args.upstream.rpartition(":")
    upstream_host, upstream_port = (host or "127.0.0.1"), int(port)

    async def forward(reader, writer):
        try:
            ur, uw = await asyncio.open_connection(upstream_host, upstream_port)
        except OSError:
            writer.close()
            return
        await asyncio.gather(pipe(reader, uw), pipe(ur, writer))
        for w in (uw, writer):
            try:
                w.close()
            except Exception:
                pass

    listen_host, _, listen_port = args.listen.rpartition(":")
    server = await asyncio.start_server(forward, listen_host or "0.0.0.0", int(listen_port))
    async with server:
        print(f"forwarding {args.listen} -> {args.upstream}", flush=True)
        await server.serve_forever()


if __name__ == "__main__":
    asyncio.run(main())
