package graph

import (
	"container/heap"
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/StevenBuglione/spice/compiler/provider"
)

// Build validates exact provider dependencies from one already validated
// catalog. It never reloads source, inspects function bodies, or executes a
// provider. A catalog that already has diagnostics is not graphed.
func Build(catalog provider.Catalog) Result {
	if len(catalog.Diagnostics()) != 0 {
		return Result{}
	}

	result := Result{providers: catalog.Providers()}
	result.nodes = make([]Node, len(result.providers))
	for index := range result.providers {
		result.nodes[index] = Node{ProviderID: result.providers[index].SymbolID, provider: &result.providers[index]}
	}

	index := buildTypeIndex(result.providers)
	adjacency := make([][]int, len(result.providers))
	for consumerIndex := range result.providers {
		consumer := &result.providers[consumerIndex]
		for _, input := range consumer.Dependencies {
			dependencyIndex, ok := index.lookup(input.Type, result.providers)
			if !ok {
				result.diagnostics = append(result.diagnostics, missingDiagnostic(consumer, input))
				continue
			}
			dependency := &result.providers[dependencyIndex]
			result.edges = append(result.edges, Edge{
				consumer:         consumer,
				dependency:       dependency,
				ConsumerID:       consumer.SymbolID,
				DependencyID:     dependency.SymbolID,
				RequiredTypeID:   input.TypeID,
				ParameterIndex:   input.Index,
				ParameterName:    input.Name,
				Position:         preferredPosition(input.Position, consumer.Position),
				PhysicalPosition: preferredPosition(input.PhysicalPosition, consumer.PhysicalPosition),
			})
			adjacency[consumerIndex] = append(adjacency[consumerIndex], dependencyIndex)
		}
	}

	sort.SliceStable(result.edges, func(i, j int) bool {
		left, right := result.edges[i], result.edges[j]
		if left.ConsumerID != right.ConsumerID {
			return left.ConsumerID < right.ConsumerID
		}
		if left.ParameterIndex != right.ParameterIndex {
			return left.ParameterIndex < right.ParameterIndex
		}
		return left.DependencyID < right.DependencyID
	})
	for index := range adjacency {
		adjacency[index] = sortedUnique(adjacency[index], result.providers)
	}

	for _, component := range stronglyConnected(adjacency, result.providers) {
		if len(component) > 1 || containsIndex(adjacency[component[0]], component[0]) {
			result.diagnostics = append(result.diagnostics, cycleDiagnostic(component, adjacency, result.providers))
		}
	}
	sortDiagnostics(result.diagnostics)
	if len(result.diagnostics) == 0 {
		result.order = constructionOrder(adjacency, result.providers)
	}
	return result
}

type typeIndex map[string][]int

func buildTypeIndex(providers []provider.Provider) typeIndex {
	index := make(typeIndex, len(providers))
	for providerIndex := range providers {
		key := semanticTypeKey(providers[providerIndex].Output)
		index[key] = append(index[key], providerIndex)
	}
	return index
}

func (index typeIndex) lookup(required types.Type, providers []provider.Provider) (int, bool) {
	for _, providerIndex := range index[semanticTypeKey(required)] {
		if types.Identical(required, providers[providerIndex].Output) {
			return providerIndex, true
		}
	}
	return 0, false
}

func semanticTypeKey(value types.Type) string {
	if value == nil {
		return "<invalid>"
	}
	value = types.Unalias(value)
	switch typed := value.(type) {
	case *types.Array:
		return fmt.Sprintf("[%d]%s", typed.Len(), semanticTypeKey(typed.Elem()))
	case *types.Slice:
		return "[]" + semanticTypeKey(typed.Elem())
	case *types.Pointer:
		return "*" + semanticTypeKey(typed.Elem())
	case *types.Map:
		return "map[" + semanticTypeKey(typed.Key()) + "]" + semanticTypeKey(typed.Elem())
	case *types.Chan:
		return fmt.Sprintf("chan%d:%s", typed.Dir(), semanticTypeKey(typed.Elem()))
	default:
		return provider.TypeID(value)
	}
}

func missingDiagnostic(consumer *provider.Provider, input provider.Dependency) Diagnostic {
	position := preferredPosition(input.Position, consumer.Position)
	physical := preferredPosition(input.PhysicalPosition, consumer.PhysicalPosition)
	return Diagnostic{
		Position:         position,
		PhysicalPosition: physical,
		ProviderID:       consumer.SymbolID,
		Kind:             "missing-dependency",
		Message: fmt.Sprintf(
			"@Bean provider %s (%s) requires exact type %s for %s, but no provider produces that type",
			providerLabel(consumer), consumer.SymbolID, input.TypeID, parameterLabel(input),
		),
	}
}

func parameterLabel(input provider.Dependency) string {
	if input.Name == "" {
		return fmt.Sprintf("parameter %d", input.Index)
	}
	return fmt.Sprintf("parameter %d %q", input.Index, input.Name)
}

func providerLabel(item *provider.Provider) string {
	if item.Symbol.DisplayLabel != "" {
		return item.Symbol.DisplayLabel
	}
	if item.Name != "" {
		return item.Name
	}
	return item.SymbolID
}

func preferredPosition(primary, fallback token.Position) token.Position {
	if primary.Filename != "" || primary.Offset != 0 || primary.Line != 0 {
		return primary
	}
	return fallback
}

func sortedUnique(values []int, providers []provider.Provider) []int {
	sort.Slice(values, func(i, j int) bool {
		return providers[values[i]].SymbolID < providers[values[j]].SymbolID
	})
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func stronglyConnected(adjacency [][]int, providers []provider.Provider) [][]int {
	indices := make([]int, len(adjacency))
	lowlinks := make([]int, len(adjacency))
	onStack := make([]bool, len(adjacency))
	for index := range indices {
		indices[index] = -1
	}
	stack := make([]int, 0, len(adjacency))
	nextIndex := 0
	var components [][]int
	var visit func(int)
	visit = func(node int) {
		indices[node], lowlinks[node] = nextIndex, nextIndex
		nextIndex++
		stack = append(stack, node)
		onStack[node] = true
		for _, dependency := range adjacency[node] {
			if indices[dependency] == -1 {
				visit(dependency)
				if lowlinks[dependency] < lowlinks[node] {
					lowlinks[node] = lowlinks[dependency]
				}
			} else if onStack[dependency] && indices[dependency] < lowlinks[node] {
				lowlinks[node] = indices[dependency]
			}
		}
		if lowlinks[node] != indices[node] {
			return
		}
		var component []int
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == node {
				break
			}
		}
		sort.Slice(component, func(i, j int) bool {
			return providers[component[i]].SymbolID < providers[component[j]].SymbolID
		})
		components = append(components, component)
	}

	order := make([]int, len(adjacency))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(i, j int) bool {
		return providers[order[i]].SymbolID < providers[order[j]].SymbolID
	})
	for _, node := range order {
		if indices[node] == -1 {
			visit(node)
		}
	}
	sort.Slice(components, func(i, j int) bool {
		return providers[components[i][0]].SymbolID < providers[components[j][0]].SymbolID
	})
	return components
}

func cycleDiagnostic(component []int, adjacency [][]int, providers []provider.Provider) Diagnostic {
	path := representativeCycle(component, adjacency, providers)
	pathIDs := make([]string, len(path))
	for index, node := range path {
		pathIDs[index] = providers[node].SymbolID
	}
	context := make([]string, len(component))
	for index, node := range component {
		context[index] = fmt.Sprintf("%s at %s", providerLabel(&providers[node]), renderPosition(providers[node].Position))
	}
	first := &providers[component[0]]
	return Diagnostic{
		Position:         first.Position,
		PhysicalPosition: first.PhysicalPosition,
		ProviderID:       first.SymbolID,
		Kind:             "cycle",
		Message: fmt.Sprintf(
			"provider dependency cycle: %s; component providers: %s",
			strings.Join(pathIDs, " -> "), strings.Join(context, ", "),
		),
	}
}

func representativeCycle(component []int, adjacency [][]int, providers []provider.Provider) []int {
	start := component[0]
	if containsIndex(adjacency[start], start) {
		return []int{start, start}
	}
	members := make(map[int]bool, len(component))
	for _, node := range component {
		members[node] = true
	}
	visited := map[int]bool{start: true}
	path := []int{start}
	var search func(int) bool
	search = func(node int) bool {
		for _, next := range adjacency[node] {
			if !members[next] {
				continue
			}
			if next == start {
				path = append(path, start)
				return true
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			path = append(path, next)
			if search(next) {
				return true
			}
			path = path[:len(path)-1]
			delete(visited, next)
		}
		return false
	}
	if search(start) {
		return path
	}
	// Tarjan guarantees a path for an SCC; retain deterministic context if a
	// future representation violates that assumption.
	fallback := append([]int(nil), component...)
	return append(fallback, component[0])
}

type constructionStats struct {
	readyComparisons int
}

type readyPriorityQueue struct {
	values      []int
	providers   []provider.Provider
	comparisons *int
}

func (queue readyPriorityQueue) Len() int { return len(queue.values) }

func (queue readyPriorityQueue) Less(i, j int) bool {
	if queue.comparisons != nil {
		*queue.comparisons++
	}
	return queue.providers[queue.values[i]].SymbolID < queue.providers[queue.values[j]].SymbolID
}

func (queue readyPriorityQueue) Swap(i, j int) {
	queue.values[i], queue.values[j] = queue.values[j], queue.values[i]
}

func (queue *readyPriorityQueue) Push(value any) {
	queue.values = append(queue.values, value.(int))
}

func (queue *readyPriorityQueue) Pop() any {
	last := len(queue.values) - 1
	value := queue.values[last]
	queue.values = queue.values[:last]
	return value
}

func constructionOrder(adjacency [][]int, providers []provider.Provider) []*provider.Provider {
	order, _ := constructionOrderWithStats(adjacency, providers)
	return order
}

func constructionOrderWithStats(adjacency [][]int, providers []provider.Provider) ([]*provider.Provider, constructionStats) {
	remaining := make([]int, len(providers))
	consumers := make([][]int, len(providers))
	for consumer, dependencies := range adjacency {
		remaining[consumer] = len(dependencies)
		for _, dependency := range dependencies {
			consumers[dependency] = append(consumers[dependency], consumer)
		}
	}
	for index := range consumers {
		consumers[index] = sortedUnique(consumers[index], providers)
	}

	stats := constructionStats{}
	ready := &readyPriorityQueue{
		values:      make([]int, 0, len(providers)),
		providers:   providers,
		comparisons: &stats.readyComparisons,
	}
	for index, count := range remaining {
		if count == 0 {
			ready.values = append(ready.values, index)
		}
	}
	heap.Init(ready)

	order := make([]*provider.Provider, 0, len(providers))
	for ready.Len() != 0 {
		node := heap.Pop(ready).(int)
		order = append(order, &providers[node])
		for _, consumer := range consumers[node] {
			remaining[consumer]--
			if remaining[consumer] == 0 {
				heap.Push(ready, consumer)
			}
		}
	}
	return order, stats
}

func containsIndex(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func renderPosition(position token.Position) string {
	filename := position.Filename
	if filename == "" {
		filename = "<unknown>"
	}
	line, column := position.Line, position.Column
	if line <= 0 {
		line = 1
	}
	if column <= 0 {
		column = 1
	}
	return fmt.Sprintf("%s:%d:%d", filename, line, column)
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.ProviderID != right.ProviderID {
			return left.ProviderID < right.ProviderID
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Message != right.Message {
			return left.Message < right.Message
		}
		if left.PhysicalPosition.Filename != right.PhysicalPosition.Filename {
			return left.PhysicalPosition.Filename < right.PhysicalPosition.Filename
		}
		return left.PhysicalPosition.Offset < right.PhysicalPosition.Offset
	})
}
