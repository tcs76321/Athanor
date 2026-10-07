"""messy.py — a deliberately over-nested module for the refactor benchmark task.

The task is to refactor this module (remove the duplicated/nested branches) AND
add the missing `classify_many` function, keeping the public API unchanged. The
hidden test asserts the public API and the new function, so the fixture alone
does not pass.
"""


def classify(n):
    if n < 0:
        return "neg"
    else:
        if n == 0:
            return "zero"
        else:
            if n > 0:
                return "pos"


def add(a, b):
    return a + b


def add_twice(a, b):
    return add(add(a, b), b)
