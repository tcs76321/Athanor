def retry(fn, attempts, base, sleep):
    last = None
    for i in range(attempts):
        try:
            return fn()
        except Exception as exc:  # noqa: BLE001 - the task retries any failure
            last = exc
            if i < attempts - 1:
                sleep(base * (2 ** i))
    raise last
