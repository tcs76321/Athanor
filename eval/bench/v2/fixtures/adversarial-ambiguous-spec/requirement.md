# Requirement (internally inconsistent)

The file exporter must satisfy both of the following criteria:

- **C1:** Every file written to `workspace/output/` must be valid UTF-8 text.
- **C2:** Every file the user drops into `inbox/` must be exported byte-for-byte,
  unchanged, including binary files (images, archives) that are not valid UTF-8.

C1 and C2 cannot both hold for a binary inbox file: exporting it byte-for-byte
violates C1, and re-encoding it to UTF-8 violates C2.
