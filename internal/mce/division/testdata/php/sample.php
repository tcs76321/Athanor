<?php

namespace Demo;

use InvalidArgumentException;

final class Counter
{
    private array $counts = [];

    public function add(string $key): void
    {
        $this->counts[$key] = ($this->counts[$key] ?? 0) + 1;
    }

    public function get(string $key): int
    {
        return $this->counts[$key] ?? 0;
    }
}
