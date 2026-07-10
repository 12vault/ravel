package lang

type Registry struct {
	analyzers map[string]Analyzer
}

func NewRegistry() *Registry {
	return &Registry{analyzers: map[string]Analyzer{}}
}

func (r *Registry) Register(a Analyzer) {
	r.analyzers[a.Language()] = a
}

func (r *Registry) ForLanguage(language string) (Analyzer, bool) {
	a, ok := r.analyzers[language]
	return a, ok
}
