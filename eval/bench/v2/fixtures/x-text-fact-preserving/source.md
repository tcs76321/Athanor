The Core Pod runs the Athanor daemon in one container. It holds the Podman
socket and spawns Job Pods without that socket. The canonical benchmark uses
ornith-1.5:9b with a 32,768-token context, a 3B cross-family judge, and a
12-hour ceiling. On 2026-10-17 the pilot completed 70 tasks in 64 minutes; 56
passed (80%), and the held-out slice passed 12 of 14. The data stratum passed
0 of 6.
