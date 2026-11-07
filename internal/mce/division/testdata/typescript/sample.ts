import { readFile } from "node:fs/promises";

interface User {
  id: string;
  name: string;
}

export async function loadUsers(path: string): Promise<User[]> {
  const raw = await readFile(path, "utf8");
  return JSON.parse(raw) as User[];
}

export function nameOf(u: User): string {
  return u.name.trim();
}
