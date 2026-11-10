from pipeline import process


def test_even_mode():
    assert process([1, 2, 3], "a") == [4]


def test_odd_mode():
    assert process([1, 2, 3], "b") == [3, 9]


def test_passthrough_mode():
    assert process([1, 2, 3], "c") == [1, 2, 3]
