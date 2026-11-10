from calc import add, mul, run


def test_add():
    assert add(2, 3) == 5


def test_mul():
    assert mul(3, 4) == 12


def test_run():
    assert run(2, 3) == 5
