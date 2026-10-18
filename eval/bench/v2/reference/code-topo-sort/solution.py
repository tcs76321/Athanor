from collections import deque


def topo_sort(graph):
    nodes = set(graph)
    for deps in graph.values():
        nodes.update(deps)
    indegree = {n: 0 for n in nodes}
    adj = {n: [] for n in nodes}
    for node, deps in graph.items():
        for dep in deps:
            adj[dep].append(node)
            indegree[node] += 1
    queue = deque(n for n in nodes if indegree[n] == 0)
    out = []
    while queue:
        node = queue.popleft()
        out.append(node)
        for nxt in adj[node]:
            indegree[nxt] -= 1
            if indegree[nxt] == 0:
                queue.append(nxt)
    if len(out) != len(nodes):
        raise ValueError("cycle")
    return out
