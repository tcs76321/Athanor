import time
from collections import OrderedDict


class LRUCache:
    def __init__(self, capacity, ttl_seconds):
        self.capacity = capacity
        self.ttl = ttl_seconds
        self.data = OrderedDict()

    def get(self, key):
        if key not in self.data:
            return None
        expires, value = self.data[key]
        if time.monotonic() >= expires:
            del self.data[key]
            return None
        self.data.move_to_end(key)
        return value

    def put(self, key, value):
        if key in self.data:
            del self.data[key]
        self.data[key] = (time.monotonic() + self.ttl, value)
        while len(self.data) > self.capacity:
            self.data.popitem(last=False)
