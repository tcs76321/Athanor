"""Line-wise three-way merge for equal-length line lists.

merge3(base, ours, theirs) returns (merged, conflicts). For each line:
- if ours == theirs, keep it;
- if ours == base, take theirs;
- if theirs == base, take ours;
- otherwise emit a conflict block and record the index.
Insertions/deletions are out of scope (the inputs must have equal length).
"""


def merge3(base, ours, theirs):
    if not (len(base) == len(ours) == len(theirs)):
        raise ValueError("merge3 requires equal-length line lists")
    merged = []
    conflicts = []
    for i, (b, o, t) in enumerate(zip(base, ours, theirs)):
        if o == t:
            merged.append(o)
        elif o == b:
            merged.append(t)
        elif t == b:
            merged.append(o)
        else:
            conflicts.append(i)
            merged.extend(["<<<<<<< ours\n", o, "=======\n", t, ">>>>>>> theirs\n"])
    return merged, conflicts
