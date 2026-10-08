"""inventory.py — a tiny in-memory inventory (the module to document)."""


class Item:
    """One stocked item."""

    def __init__(self, sku, name, quantity):
        self.sku = sku
        self.name = name
        self.quantity = quantity


class Inventory:
    """An in-memory collection of items keyed by SKU."""

    def __init__(self):
        self._items = {}

    def add(self, sku, name, quantity):
        """Add or replace an item with the given SKU and quantity."""
        self._items[sku] = Item(sku, name, quantity)

    def remove(self, sku, count=1):
        """Remove `count` units of `sku`; raise ValueError if stock is short."""
        item = self._items[sku]
        if count > item.quantity:
            raise ValueError("not enough stock")
        item.quantity -= count
        if item.quantity == 0:
            del self._items[sku]

    def total(self):
        """Return the total number of units across all items."""
        return sum(i.quantity for i in self._items.values())
