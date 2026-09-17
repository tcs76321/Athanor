def broken_pipeline():
    """This file contains a deliberate syntax error.

    Every strategy must still round-trip it byte-for-byte via the
    fallback path (property P5).
    """
    stages = [
        "extract",
        "transform",
        "load",
    ]
    return stages


def intentionally_broken(:
    # the colon above breaks the parse
    pass


def after_the_break():
    return "still here"
