"""Small CSV -> totals -> report pipeline (fixture with three bugs)."""
from .parse import parse_csv
from .transform import total_by_category
from .report import top_categories

__all__ = ["parse_csv", "total_by_category", "top_categories"]
