from shop import Item, total


def test_total_without_discount():
    assert total([Item(10), Item(5)]) == 15


def test_total_with_discount():
    assert total([Item(10, discount=0.5), Item(5)]) == 10


def test_discount_is_clamped():
    assert total([Item(10, discount=2.0)]) == 10
