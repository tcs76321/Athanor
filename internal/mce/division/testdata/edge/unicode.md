# Unicode Fixture — 多字节

This file mixes multi-byte characters with structural headers. Byte
offsets are not character offsets, so line and byte metadata must be
computed on bytes, not runes.

## Ärger über Größe

Straße, café, naïve, 日本語, emoji 🧪, and a combining mark: e\u0301.

## Заключение

Конец документа.