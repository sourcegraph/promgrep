package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type metricKind int

const (
	gauge metricKind = iota
	histogram
	counter
)

func (kind metricKind) String() string {
	switch kind {
	case gauge: return "Gauge"
	case histogram: return "Histogram"
	case counter: return "Counter"
	}
	return ""
}

type promOpts map[string]string

type matchResult struct {
	// score is in range [0..100], bigger is better match, 100 is perfect match
	score int
	path string
	val string
	help string
	line int
	kind metricKind
}

type byScore []matchResult

func (a byScore) Len() int           { return len(a) }
func (a byScore) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byScore) Less(i, j int) bool { return a[i].score > a[j].score }

type matcher interface {
	Match(opts promOpts, pos token.Position) (matchResult, bool)
}

type matchAny struct{}

func (ma *matchAny) Match(opts promOpts, pos token.Position) (matchResult, bool) {
	return matchResult{
		score: -1,
		path:  pos.Filename,
		line:  pos.Line,
		help:  opts["Help"],
		val: qualifiedMetricName(opts),
	}, true
}

type matchName struct {
	name string
}

func (mn *matchName) Match(opts promOpts, pos token.Position) (matchResult, bool) {
	if opts["Namespace"] != "" && opts["Subsystem"] == "" &&
		len(mn.name) > (len(opts["Namespace"]) + len(opts["Name"])) {
		if !strings.HasPrefix(mn.name, opts["Namespace"]) || !strings.HasSuffix(mn.name, opts["Name"]) {
			return matchResult{}, false
		}

		delta, denum := len(mn.name) - len(opts["Namespace"]) - len(opts["Name"]), len(mn.name)
		score := 100 - delta * 100 / denum
		return matchResult{
			score: score,
			path:  pos.Filename,
			line:  pos.Line,
			val:   qualifiedMetricName(opts),
			help:  opts["Help"],
		}, true
	}
	qmn := qualifiedMetricName(opts)

	if !strings.Contains(mn.name, qmn) && !strings.Contains(qmn, mn.name){
		return matchResult{}, false
	}

	delta, denum := len(mn.name) - len(qmn), len(mn.name)
	if delta < 0 {
		delta, denum = -delta, len(qmn)
	}

	score := 100 - delta * 100 / denum
	return matchResult{
		score: score,
		path:  pos.Filename,
		line:  pos.Line,
		val:   qualifiedMetricName(opts),
		help:  opts["Help"],
	}, true
}

func getCallExprLiteral(c *ast.CallExpr) string {
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}

	i, ok := s.X.(*ast.Ident)
	if !ok {
		return ""
	}

	return i.Name + "." + s.Sel.Name
}

func unquote(val string) string {
	n := len(val)
	if n < 2 || val[0] != '"' || val[n-1] != '"' {
		return val
	}
	return val[1:n-1]
}

func qualifiedMetricName(opts promOpts) string {
	qmn := opts["Name"]

	if opts["Subsystem"] != "" {
		qmn = opts["Subsystem"] + "_" + qmn
	}
	if opts["Namespace"] != "" && opts["Subsystem"] != "" {
		qmn = opts["Namespace"] + "_" + qmn
	}
	return qmn
}

func getOpts(c *ast.CallExpr) promOpts {
	opts := make(promOpts)
	cl, ok := c.Args[0].(*ast.CompositeLit)
	if !ok {
		return opts
	}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		val, ok := kv.Value.(*ast.BasicLit)
		if !ok {
			continue
		}

		opts[key.Name] = unquote(val.Value)
	}
	return opts
}

var constructors = map[string]metricKind {
	"prometheus.NewCounterVec": counter,
	"prometheus.NewCounter": counter,
	"prometheus.NewHistogramVec": histogram,
	"prometheus.NewHistogram": histogram,
	"prometheus.NewGaugeVec": gauge,
	"prometheus.NewGauge": gauge,
	"promauto.NewCounterVec": counter,
	"promauto.NewCounter": counter,
	"promauto.NewHistogramVec": histogram,
	"promauto.NewHistogram": histogram,
	"promauto.NewGaugeVec": gauge,
	"promauto.NewGauge": gauge,
}

func inspect(fset *token.FileSet, node ast.Node, mr matcher, accum *byScore) error {
	callExpr, ok := node.(*ast.CallExpr)
	if !ok {
		return nil
	}

	name := getCallExprLiteral(callExpr)

	kind, ok := constructors[name]
	if ok {
		opts := getOpts(callExpr)

		hit, ok := mr.Match(opts, fset.Position(node.Pos()))

		if ok {
			hit.kind = kind
			*accum = append(*accum, hit)
		}
	}
	return nil
}

func process(path string, mr matcher, accum *byScore) error {
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}

	found := false
	for _, ispec := range tree.Imports {
		if unquote(ispec.Path.Value) == "github.com/prometheus/client_golang/prometheus" {
			found = true
			break
		}
	}

	if !found {
		return nil
	}

	tree, err = parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}

	ast.Inspect(tree, func(node ast.Node) bool {
		err := inspect(fset, node, mr, accum)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error inspecting AST for %s: %v", path, err)
		}
		return err == nil
	})
	return nil
}

func main() {
	if len(os.Args) > 2 {
		fmt.Println(`
Usage:
    promgrep                         (lists declarations of all metrics)
    promgrep some:metric:name        (searches for declaration of some:metric:name`)
		os.Exit(0)
	}

	var mr matcher
	var accum byScore

	mr = &matchAny{}
	if len(os.Args) == 2 {
		mr = &matchName{name:os.Args[1]}
	}

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		return process(path, mr, &accum)
	})

	if err != nil {
		log.Fatal(err)
	}

	sort.Sort(accum)

	for _, hit := range accum {
		if hit.score == -1 {
			fmt.Printf("%s:%d    %s %s: %s\n", hit.path, hit.line, hit.val, hit.kind, hit.help)
		} else {
			fmt.Printf("%s:%d    %s %s score:%d\n", hit.path, hit.line, hit.val, hit.kind, hit.score)
		}
	}
}
