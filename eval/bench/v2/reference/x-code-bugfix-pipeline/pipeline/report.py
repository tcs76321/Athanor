def top_categories(totals, n):
    return sorted(totals, key=lambda category: (-totals[category], category))[:n]
