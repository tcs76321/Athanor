// scheduler.js — hand-written fixture for the M5-T1 division spike.
// Shaped like real code: imports, classes, factory functions, template
// literals containing declaration-shaped text, braces inside strings,
// and comments containing braces. A naive brace counter will cut inside
// strings or comments; a naive regex will cut on column-zero text inside
// template literals.

import { EventEmitter } from "events";

const DEFAULT_CONCURRENCY = 4;
const STATE_QUEUED = "queued";
const STATE_RUNNING = "running";
const STATE_DONE = "done";
const STATE_FAILED = "failed";

const TEMPLATE_SHAPED = `
Everything in this template literal is text, including column-zero
declaration-shaped lines:

function fakeInsideTemplate() {
  return "not real";
}

const fakeConst = { braces: { inside: true } };

A brace counter that does not track template state ends here.
`;

class Task extends EventEmitter {
  constructor(id, payload) {
    super();
    this.id = id;
    this.payload = payload;
    this.state = STATE_QUEUED;
    this.attempts = 0;
  }

  describe() {
    return `Task(id=${this.id}, state=${this.state}, attempts=${this.attempts})`;
  }

  mark(state) {
    this.state = state;
    this.emit("change", this);
  }
}

class Scheduler {
  constructor(concurrency = DEFAULT_CONCURRENCY) {
    this.concurrency = concurrency;
    this.queue = [];
    this.running = new Map();
    this.completed = [];
  }

  enqueue(task) {
    this.queue.push(task);
    task.mark(STATE_QUEUED);
    return this;
  }

  drain() {
    while (this.running.size < this.concurrency && this.queue.length > 0) {
      const task = this.queue.shift();
      this.running.set(task.id, task);
      task.mark(STATE_RUNNING);
      setImmediate(() => this.finish(task));
    }
    return this.running.size;
  }

  finish(task) {
    this.running.delete(task.id);
    // A comment containing braces must not confuse a brace counter:
    // function fakeInComment() { if (x) { return "}"; } }
    const outcome = task.payload ? STATE_DONE : STATE_FAILED;
    task.mark(outcome);
    this.completed.push({ id: task.id, outcome });
  }

  stats() {
    return {
      queued: this.queue.length,
      running: this.running.size,
      done: this.completed.filter((c) => c.outcome === STATE_DONE).length,
      failed: this.completed.filter((c) => c.outcome === STATE_FAILED).length,
    };
  }
}

/* A block comment with unbalanced-looking braces: { { { } } }
   and a fake function:
   function alsoFake() {
     return '""\'"';
   }
   End of block comment. */

function makeTask(id, payload) {
  const spec = { id, payload, created: Date.now() };
  const nested = { spec, meta: { retry: { max: 3 } } };
  return new Task(id, nested);
}

function serializeStats(scheduler) {
  const stats = scheduler.stats();
  return JSON.stringify({ ...stats, ts: Date.now() }, null, 2);
}

const SINGLE_QUOTE_BRACE = 'a string with a } and a { inside';
const DOUBLE_QUOTE_BRACE = "another }{ tricky } string";

async function runAll(scheduler, specs) {
  for (const spec of specs) {
    scheduler.enqueue(makeTask(spec.id, spec.payload));
  }
  scheduler.drain();
  return serializeStats(scheduler);
}

export { Scheduler, Task, makeTask, runAll, TEMPLATE_SHAPED };
