def search_rotated(xs, target):
    lo, hi = 0, len(xs) - 1
    while lo <= hi:
        mid = (lo + hi) // 2
        if xs[mid] == target:
            return mid
        if xs[lo] == xs[mid] == xs[hi]:
            lo += 1
            hi -= 1
        elif xs[lo] <= xs[mid]:
            if xs[lo] <= target < xs[mid]:
                hi = mid - 1
            else:
                lo = mid + 1
        else:
            if xs[mid] < target <= xs[hi]:
                lo = mid + 1
            else:
                hi = mid - 1
    return -1
