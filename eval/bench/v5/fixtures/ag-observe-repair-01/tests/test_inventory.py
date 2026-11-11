from inventory.models import Item
from inventory.service import Inventory


def test_add_accumulates():
    inv = Inventory()
    inv.add(Item("bolt", 2, 3))
    inv.add(Item("bolt", 4, 3))
    assert inv.quantity("bolt") == 6


def test_total_multiplies_quantity():
    inv = Inventory()
    inv.add(Item("nut", 5, 7))
    assert inv.total("nut") == 35


def test_missing_item_is_zero():
    inv = Inventory()
    assert inv.quantity("ghost") == 0
    assert inv.total("ghost") == 0
