def parse_csv(text):
    lines = text.strip().split("\n")
    header = lines[0].split(",")
    return [dict(zip(header, line.split(","))) for line in lines[1:]]
