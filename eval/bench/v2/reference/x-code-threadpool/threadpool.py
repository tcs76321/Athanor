"""Bounded thread pool with a future-like result handle."""

import concurrent.futures


class Future:
    def __init__(self, inner):
        self._inner = inner

    def result(self, timeout=None):
        return self._inner.result(timeout)

    def done(self):
        return self._inner.done()

    def cancel(self):
        return self._inner.cancel()


class ThreadPool:
    def __init__(self, workers):
        if workers <= 0:
            raise ValueError("workers must be positive")
        self._executor = concurrent.futures.ThreadPoolExecutor(max_workers=workers)
        self._closed = False

    def submit(self, fn, *args, **kwargs):
        if self._closed:
            raise RuntimeError("pool is shut down")
        return Future(self._executor.submit(fn, *args, **kwargs))

    def shutdown(self, wait=True):
        self._closed = True
        self._executor.shutdown(wait=wait)
