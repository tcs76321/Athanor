def process(xs, mode):
    out = []
    for x in xs:
        if mode == "a":
            if x % 2 == 0:
                out.append(x * 2)
        elif mode == "b":
            if x % 2 == 1:
                out.append(x * 3)
        else:
            out.append(x)
    return out
