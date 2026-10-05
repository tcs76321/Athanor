# Anchor cases

Artifacts for human rating. Blind to arm and model. Rate per
[`README.md`](README.md).

---

## md-d1

- **Goal:** Write a short README with exactly three sections named Install, Usage, and License.
- **Criteria:** sections named Install, Usage, License; a one-line project description at the top

~~~~
# md2html-readme-ornith9b-dialectical-r1-866000

Converts Markdown source into clean HTML documents with zero dependencies.

## Install

```bash
npm install
```

## Usage

```bash
npx md2html README.md
```

## License

Distributed under the MIT License. See LICENSE for details.
~~~~

---

## md-d2

- **Goal:** Write a short README with exactly three sections named Install, Usage, and License.
- **Criteria:** sections named Install, Usage, License; a one-line project description at the top

~~~~
# md2html-readme-ornith9b-dialectical-r2-221000

A lightweight Markdown-to-HTML converter for rendering README files in the
ornith9b dialect, optimized for fast, readable document transforms.

## License

This project is licensed under the MIT License. See the accompanying
`LICENSE` file for the full text.

## Install

Install the package with your preferred package manager:

```sh
pip install md2html-readme-ornith9b-dialectical-r2-221000
```

## Usage

Render a Markdown file to HTML with a single command:

```sh
md2html-readme-ornith9b-dialectical-r2-221000 input.md > output.html
```

LIMITATION: The README references an accompanying `LICENSE` file and a `LICENSE` file for full text that are not included in this artifact.
~~~~

---

## md-d3

- **Goal:** Write a short README with exactly three sections named Install, Usage, and License.
- **Criteria:** sections named Install, Usage, License; a one-line project description at the top

~~~~
# md2html

A small utility that converts Markdown README files into clean, semantic HTML.

## Install

```bash
npm install -g md2html
```

## Usage

```bash
md2html README.md > README.html
```

## License

MIT
~~~~

---

## md-s1

- **Goal:** Write a short README with exactly three sections named Install, Usage, and License.
- **Criteria:** sections named Install, Usage, License; a one-line project description at the top

~~~~
# md2html-readme-ornith9b-single-r1-297000

Convert Markdown to clean HTML in a single command.

## Install

```bash
pip install md2html-readme-ornith9b-single-r1-297000
```

## Usage

```bash
md2html readme.md > readme.html
```

## License

MIT
~~~~

---

## sa-d1

- **Goal:** Write a short design brief with exactly three sections: Parts, Steps, and Risks.
- **Criteria:** sections named Parts, Steps, Risks; at least two risks named

~~~~
# Design Brief: Sunrise Alarm System

## Parts
1. **Alarm Module** — the core engine that generates the wake-up signal (sunrise time computation + trigger).
2. **Sensor Interface** — reads environmental/time inputs (e.g., a light or time-of-day sensor).
3. **Display/Output** — surface that presents the alarm state to the user.

## Steps
1. Compute the target sunrise time from the user's location and date.
2. Feed the computed time into the Alarm Module and schedule it.
3. When the scheduled time arrives, the Alarm Module activates the output.
4. Confirm activation on the Display/Output and log the event.

## Risks
1. **Timezone/Location Drift** — inaccurate sunrise times if the user's coordinates or DST rules are stale, causing the alarm to fire at the wrong time.
2. **Sensor Failure** — if the time-of-day sensor loses accuracy or fails, the computed sunrise time may be wrong, silently breaking the alarm.
~~~~

---

## sa-d2

- **Goal:** Write a short design brief with exactly three sections: Parts, Steps, and Risks.
- **Criteria:** sections named Parts, Steps, Risks; at least two risks named

~~~~
**PARTS**

1. **The Sun** — a fixed, known light source that rises at a predictable time each morning.
2. **The Sleeper** — a person who must be awake by a target time.
3. **The Alarm** — a mechanism (sound, light, or both) that interrupts sleep at the target time.

**STEPS**

1. Observe the sun to determine the exact moment it appears at the horizon.
2. Decide on a wake-up time and calculate the time difference from sunrise.
3. Set the alarm to trigger at the wake-up time.
4. When the target time arrives, the alarm activates to signal the sleeper.

**RISKS**

1. **Cloud cover** — overcast or foggy skies can delay or hide the sun, making it an unreliable reference for time.
2. **Mechanical failure** — the alarm may not fire (dead battery, stuck device), leaving the sleeper unawake.

LIMITATION: This artifact was produced from the provided candidate proposal without access to any additional design context, requirements, or constraints; no external refinement was performed.
~~~~

---

## sa-d3

- **Goal:** Write a short design brief with exactly three sections: Parts, Steps, and Risks.
- **Criteria:** sections named Parts, Steps, Risks; at least two risks named

~~~~
**Design Brief — sunrise-alarm-brief-ornith9b-dialectical-r3-684000**

**Parts**
- Alarm mechanism: the core trigger that fires when a threshold is crossed.
- Morning light: the environmental cue tied to sunrise timing.
- Brief document: the three-section artifact (Parts, Steps, Risks) itself.

**Steps**
1. Identify the sunrise time and the alarm's trigger threshold.
2. Wire the alarm to respond to the morning light cue.
3. Test the trigger against the threshold under varying conditions.
4. Deliver the brief containing the three required sections.

**Risks**
- The alarm may fire too early if the light cue is misread.
- Sunrise time can shift seasonally, breaking a fixed threshold.
- A brief document that omits one of the three sections fails the spec.

LIMITATION: The task provides no substantive subject matter beyond the artifact structure itself; the brief describes a metaphorical "sunrise alarm" system rather than a concrete deliverable, so Parts, Steps, and Risks are illustrative rather than grounded in a defined project.
~~~~

---

## sa-s1

- **Goal:** Write a short design brief with exactly three sections: Parts, Steps, and Risks.
- **Criteria:** sections named Parts, Steps, Risks; at least two risks named

~~~~
# Sunrise Alarm Design Brief

## Parts
- **Alarm Module**: the component that emits the wake signal (sound/light/vibration).
- **Scheduler Module**: the component that determines when the alarm should fire.
- **Interface Layer**: the surface through which the user sets, views, and adjusts the alarm.

## Steps
1. Define the alarm requirements (time, volume, repeat rules, user preferences).
2. Implement the Scheduler Module to compute fire times from those requirements.
3. Implement the Alarm Module to emit the chosen wake signal at the scheduled time.
4. Build the Interface Layer so users can set and review alarms.
5. Integrate the modules and verify end-to-end that the alarm fires correctly.

## Risks
- **Timing drift**: the scheduled fire time may drift from the intended time if the system clock is not accurate or is not adjusted for time zones and daylight saving transitions.
- **User misconfiguration**: unclear interface wording could cause users to set an incorrect alarm time or repeat pattern, leading to failed wake-ups.
~~~~
