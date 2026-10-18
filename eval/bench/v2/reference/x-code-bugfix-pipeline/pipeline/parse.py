import csv
import io


def parse_csv(text):
    return [dict(row) for row in csv.DictReader(io.StringIO(text))]
