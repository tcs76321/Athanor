import React from "react";

type Props = { title: string; count: number };

export function Badge({ title, count }: Props) {
  return (
    <span className="badge">
      {title}: {count}
    </span>
  );
}
