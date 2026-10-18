def parse_csv(text):
    rows = []
    row = []
    field = []
    in_quotes = False
    i = 0
    while i < len(text):
        c = text[i]
        if in_quotes:
            if c == '"':
                if i + 1 < len(text) and text[i + 1] == '"':
                    field.append('"')
                    i += 2
                    continue
                in_quotes = False
                i += 1
                continue
            field.append(c)
            i += 1
            continue
        if c == '"':
            in_quotes = True
        elif c == ",":
            row.append("".join(field))
            field = []
        elif c == "\n":
            row.append("".join(field))
            rows.append(row)
            row = []
            field = []
        elif c != "\r":
            field.append(c)
        i += 1
    if field or row:
        row.append("".join(field))
        rows.append(row)
    return rows
