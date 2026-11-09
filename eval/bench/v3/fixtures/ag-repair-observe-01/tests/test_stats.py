from observe import mean


def test_mean_nonempty():
    assert mean([2, 4, 6]) == 4


def test_mean_empty_is_zero():
    assert mean([]) == 0.0
