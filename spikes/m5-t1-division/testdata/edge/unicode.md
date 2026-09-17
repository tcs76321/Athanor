# Unicode Edge Case — Division Spike

This file mixes multi-byte characters with structural headers. Byte
offsets and rune offsets differ here; a strategy that confuses them
will misplace boundaries while still tiling, so byte identity alone is
not enough — the line metadata must also stay correct (P3).

## Café section

Emoji, CJK, and combining marks: 你好世界 — 𐐷 — é — 🙂.

```python
# déjà vu: a comment inside a fence
def café():
    return "𝔘𝔫𝔦𝔠𝔬𝔡𝔢"
```

## Математика

Формула: ∑ᵢ aᵢ² = ‖a‖². Заголовок для проверки границ.

## Заключение

The file ends with a line containing multibyte characters: ✅ ✔️ ☑️.
