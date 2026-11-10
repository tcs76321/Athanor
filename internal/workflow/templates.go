package workflow

// Built-in templates are starting points, not privileged workflows: they are
// ordinary graphs the canvas opens for editing. A template is versioned so a
// project can keep running the version it approved while a newer one is edited.

// DailyBriefTemplate is the operating cycle: find today's sources, extract and
// condense them with citations, write the brief, verify it, and record what was
// covered so the next run does not repeat it. The schedule and manual triggers
// are data; the trigger runner is a later phase.
func DailyBriefTemplate() Template {
	return Template{
		ID: "daily-brief", Name: "Daily Brief", Version: 1, CycleKind: "operate",
		Graph: Graph{
			Blocks: []Block{
				{ID: "research", Kind: KindResearch, Title: "Find today's sources",
					Config: map[string]any{"instructions": "Focus on what changed in the last 24 hours. Prefer primary sources over commentary."}},
				{ID: "fetch", Kind: KindFetch, Title: "Fetch the sources"},
				{ID: "extract", Kind: KindExtract, Title: "Extract dated items"},
				{ID: "condense", Kind: KindCondense, Title: "Condense the items"},
				{ID: "synthesize", Kind: KindSynthesize, Title: "Write the brief",
					Config: map[string]any{"instructions": "A short brief: what changed, why it matters, and the source for each point."}},
				{ID: "verify", Kind: KindVerify, Title: "Check citations and criteria"},
				{ID: "retain", Kind: KindRetain, Title: "Remember what was covered"},
			},
			Edges: []Edge{
				{From: "research", To: "fetch"},
				{From: "fetch", To: "extract"},
				{From: "extract", To: "condense"},
				{From: "condense", To: "synthesize"},
				{From: "synthesize", To: "verify"},
				{From: "synthesize", To: "retain"},
			},
		},
		Triggers: []Trigger{
			{ID: "schedule", Kind: TriggerSchedule, Spec: "0 7 * * *", Enabled: true},
			{ID: "manual", Kind: TriggerManual, Enabled: true},
		},
	}
}

// DiscoveryTemplate is the cycle-0 graph for a goal that is not yet defined:
// research the space, generate distinct directions, critique them, and let the
// human select one.
func DiscoveryTemplate() Template {
	return Template{
		ID: "discovery", Name: "Discovery", Version: 1, CycleKind: "discovery",
		Graph: Graph{
			Blocks: []Block{
				{ID: "research", Kind: KindResearch, Title: "Research the space"},
				{ID: "options", Kind: KindGenerateOptions, Title: "Generate three directions"},
				{ID: "critique", Kind: KindCritique, Title: "Critique the directions"},
				{ID: "select", Kind: KindSelect, Title: "Choose a direction"},
			},
			Edges: []Edge{
				{From: "research", To: "options"},
				{From: "options", To: "critique"},
				{From: "critique", To: "select"},
			},
		},
		Triggers: []Trigger{{ID: "manual", Kind: TriggerManual, Enabled: true}},
	}
}

// BuildTemplate is a small, reviewable implementation cycle.
func BuildTemplate() Template {
	return Template{
		ID: "build", Name: "Build", Version: 1, CycleKind: "build",
		Graph: Graph{
			Blocks: []Block{
				{ID: "implement", Kind: KindImplement, Title: "Implement the first slice"},
				{ID: "test", Kind: KindTest, Title: "Run the tests"},
				{ID: "review", Kind: KindReview, Title: "Review the change"},
			},
			Edges: []Edge{
				{From: "implement", To: "test"},
				{From: "test", To: "review"},
			},
		},
		Triggers: []Trigger{{ID: "manual", Kind: TriggerManual, Enabled: true}},
	}
}

// NewsTemplate is the operating cycle for a local-first news wire: read the
// wire files, extract and dedupe items, write the report, verify it, and
// record what was covered. It uses read_file, not the network.
func NewsTemplate() Template {
	return Template{
		ID: "news-report", Name: "News Report", Version: 1, CycleKind: "operate",
		Graph: Graph{
			Blocks: []Block{
				{ID: "research", Kind: KindResearch, Title: "Read the wire",
					Config: map[string]any{"instructions": "Read the wire files under wire/ with read_file. Do not use the network. List the story ids you found."}},
				{ID: "fetch", Kind: KindFetch, Title: "Load the wire files"},
				{ID: "extract", Kind: KindExtract, Title: "Extract wire items"},
				{ID: "condense", Kind: KindCondense, Title: "Dedupe and rank",
					Config: map[string]any{"instructions": "Merge duplicate story ids across sources. Mark updates to an earlier story as updates, not new stories."}},
				{ID: "synthesize", Kind: KindSynthesize, Title: "Write the brief",
					Config: map[string]any{"instructions": "Use the headings New, Previously reported, and Sources. Cite the wire item id for every item. If there is nothing new, say so."}},
				{ID: "verify", Kind: KindVerify, Title: "Check citations and coverage"},
				{ID: "retain", Kind: KindRetain, Title: "Remember what was covered"},
			},
			Edges: []Edge{
				{From: "research", To: "fetch"},
				{From: "fetch", To: "extract"},
				{From: "extract", To: "condense"},
				{From: "condense", To: "synthesize"},
				{From: "synthesize", To: "verify"},
				{From: "synthesize", To: "retain"},
			},
		},
		Triggers: []Trigger{
			{ID: "schedule", Kind: TriggerSchedule, Spec: "0 * * * *", Enabled: true},
			{ID: "manual", Kind: TriggerManual, Enabled: true},
		},
	}
}

// Templates returns the built-ins in a stable order.
func Templates() []Template {
	return []Template{DailyBriefTemplate(), DiscoveryTemplate(), BuildTemplate(), NewsTemplate()}
}

// TemplateByID resolves a built-in by ID.
func TemplateByID(id string) (Template, bool) {
	for _, t := range Templates() {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}
