// A tiny scheduler fixture. Braces appear inside comments and template
// literals so a structural scanner has to be careful; the shipped hybrid
// uses tree-sitter for JavaScript, so this exercises real AST boundaries.
import { setTimeout as delay } from "node:timers/promises";

const DEFAULTS = {
  retries: 3,
};

/** Format a task for logging. */
function describe(task) {
  return `task ${task.name} {attempt=${task.attempt}}`;
}

export class Scheduler {
  constructor(tasks, options = DEFAULTS) {
    this.tasks = tasks;
    this.options = options;
  }

  async run() {
    for (const task of this.tasks) {
      await this.execute(task);
    }
  }

  async execute(task) {
    for (let attempt = 1; attempt <= this.options.retries; attempt++) {
      try {
        return await task.run();
      } catch (err) {
        if (attempt === this.options.retries) throw err;
        await delay(10 * attempt);
      }
    }
  }
}

export const describeAll = (tasks) => tasks.map(describe).join("\n");