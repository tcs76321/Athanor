import bisect


def max_weight(intervals):
    if not intervals:
        return 0
    iv = sorted(intervals, key=lambda x: x[1])
    ends = [x[1] for x in iv]
    best = [0] * (len(iv) + 1)
    for i in range(1, len(iv) + 1):
        start, _end, weight = iv[i - 1]
        j = bisect.bisect_right(ends, start, 0, i - 1)
        best[i] = max(best[i - 1], best[j] + weight)
    return best[-1]
