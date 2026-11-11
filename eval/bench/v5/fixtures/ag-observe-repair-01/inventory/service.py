from .models import Item


class Inventory:
    def __init__(self):
        self._items = {}

    def add(self, item: Item) -> None:
        self._items[item.name] = item

    def quantity(self, name: str) -> int:
        item = self._items.get(name)
        return item.quantity if item else 0

    def total(self, name: str) -> int:
        item = self._items.get(name)
        return item.quantity if item else 0
