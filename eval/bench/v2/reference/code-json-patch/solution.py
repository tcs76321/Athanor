import copy


def _parts(path):
    if not path.startswith("/"):
        raise ValueError("bad path")
    return [p.replace("~1", "/").replace("~0", "~") for p in path[1:].split("/")]


def _get(doc, parts):
    cur = doc
    for part in parts:
        if isinstance(cur, list):
            cur = cur[int(part)]
        else:
            cur = cur[part]
    return cur


def apply_patch(doc, patch):
    out = copy.deepcopy(doc)
    for op in patch:
        parts = _parts(op["path"])
        kind = op["op"]
        if kind == "test":
            if _get(out, parts) != op["value"]:
                raise ValueError("test failed")
        elif kind in ("replace", "add", "remove"):
            parent = _get(out, parts[:-1])
            key = parts[-1]
            if kind == "replace":
                if isinstance(parent, list):
                    parent[int(key)] = op["value"]
                else:
                    parent[key] = op["value"]
            elif kind == "add":
                if isinstance(parent, list):
                    parent.insert(int(key), op["value"])
                else:
                    parent[key] = op["value"]
            else:
                if isinstance(parent, list):
                    parent.pop(int(key))
                else:
                    del parent[key]
        else:
            raise ValueError("unsupported op")
    return out
