import heapq
import itertools


class PriorityQueue:
    def __init__(self):
        self._heap = []
        self._counter = itertools.count()

    def push(self, item, priority):
        heapq.heappush(self._heap, (-priority, next(self._counter), item))

    def pop(self):
        if not self._heap:
            raise IndexError("pop from an empty priority queue")
        return heapq.heappop(self._heap)[2]
