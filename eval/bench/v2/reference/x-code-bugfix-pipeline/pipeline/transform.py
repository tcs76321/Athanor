def total_by_category(rows):
    totals = {}
    for row in rows:
        category = row["category"]
        totals[category] = totals.get(category, 0) + int(row["amount"])
    return totals
