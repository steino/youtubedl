package youtubedl

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/patrickmn/go-cache"
)

type Player struct {
	httpClient      *http.Client
	sig_timestamp   int
	sig_sc          string
	nsig_sc         string
	nsig_name       string
	nsig_check      string
	visitorData     string
	global_variable *FindVariableResult
}

var (
	playerRe              = regexp.MustCompile(`(?m)player\\\/(\w+)\\/`)
	signatureTimestampRe  = regexp.MustCompile(`(?m)signatureTimestamp:(\d+),`)
	signatureSourceCodeRe = regexp.MustCompile(`(?m)function\(([A-Za-z_0-9]+)\)\{([A-Za-z_0-9]+=[A-Za-z_0-9]+\.split\((?:[^)]+)\)(.+?)\.join\((?:[^)]+)\))\}`)
	nsigCheckRe           = regexp.MustCompile(`(?m)if\(typeof (.+)\=\=\=.+\)return`)

	nsigCache   = cache.New(-1, -1)
	playerCache = cache.New(5*time.Minute, 10*time.Minute)
)

func NewPlayer() (player *Player, err error) {
	uri, err := url.Parse(URLs.YTBase)
	if err != nil {
		return
	}

	visitorData, err := getVisitorData()
	if err != nil {
		return
	}

	player = new(Player)
	player.httpClient = &http.Client{}
	player.visitorData = visitorData

	uri.Path = path.Join(uri.Path, "/iframe_api")

	resp, err := player.httpClient.Get(uri.String())
	if err != nil {
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	if !playerRe.Match(body) {
		return
	}

	matches := playerRe.FindSubmatch(body)

	player_id := string(matches[1])

	playerc, found := playerCache.Get(player_id)
	if found {
		return playerc.(*Player), nil
	}

	player_uri, err := url.Parse(URLs.YTBase)
	if err != nil {
		return
	}

	player_uri.Path = path.Join(player_uri.Path, fmt.Sprintf("/s/player/%s/player_ias.vflset/en_US/base.js", player_id))
	req, err := http.NewRequest("GET", player_uri.String(), nil)
	if err != nil {
		return
	}
	req.Header.Add("User-Agent", RandomUserAgent())

	player_resp, err := player.httpClient.Do(req)
	if err != nil {
		return
	}

	player_js, err := io.ReadAll(player_resp.Body)
	if err != nil {
		return
	}

	player.global_variable, err = extractGlobalVariable(string(player_js))
	if err != nil {
		return
	}

	player.sig_timestamp, err = extractSigTimestamp(string(player_js))
	if err != nil {
		return
	}

	player.sig_sc, err = extractSigSourceCode(string(player_js), player.global_variable)
	if err != nil {
		return
	}

	player.nsig_name, player.nsig_sc, err = extractNSigSourceCode(string(player_js), player.global_variable)
	if err != nil {
		return
	}

	nsig_check := nsigCheckRe.FindStringSubmatch(player.nsig_sc)
	if len(nsig_check) > 0 {
		player.nsig_check = nsig_check[1]
	}

	playerCache.Set(player_id, player, cache.DefaultExpiration)

	return
}

func (p *Player) decipher(uri string, cipher string) (code string, err error) {
	parsed_uri, err := url.Parse(uri)
	if err != nil {
		return
	}

	if uri == "" && p.sig_sc != "" && cipher != "" {
		tmp := &url.URL{}
		tmp.RawQuery = cipher
		query := tmp.Query()

		parsed_uri, err = url.Parse(query.Get("url"))
		if err != nil {
			return "", err
		}

		s := query.Get("s")
		vm := goja.New()
		vm.Set("sig", s)
		sig, err := vm.RunString(p.sig_sc)
		if err != nil {
			return "", err
		}

		query2 := parsed_uri.Query()
		sp := query.Get("sp")
		if sp != "" {
			query2.Set(sp, sig.String())
		} else {
			query2.Set("sig", sig.String())
		}

		parsed_uri.RawQuery = query2.Encode()
	}
	query := parsed_uri.Query()

	n := query.Get("n")
	if p.nsig_sc != "" && n != "" {
		nsig, found := nsigCache.Get(n)
		if !found {
			vm := goja.New()
			err := vm.Set(p.nsig_check, true)
			if err != nil {
				return "", err
			}
			_, err = vm.RunString(p.nsig_sc)
			if err != nil {
				return "", err
			}

			var decipher func(string) string
			err = vm.ExportTo(vm.Get(p.nsig_name), &decipher)
			if err != nil {
				return "", err
			}

			nsig = decipher(n)
			nsigCache.Set(n, nsig, -1)
		}

		query.Set("n", nsig.(string))

	}

	client := query.Get("c")
	switch client {
	case "WEB":
		query.Set("cver", Clients["WEB"].Version)
	case "MWEB":
		query.Set("cver", Clients["MWEB"].Version)
	case "WEB_REMIX":
		query.Set("cver", Clients["YTMUSIC"].Version)
	case "WEB_KIDS":
		query.Set("cver", Clients["WEB_KIDS"].Version)
	case "TVHTML5":
		query.Set("cver", Clients["TV"].Version)
	case "TVHTML5_SIMPLY_EMBEDDED_PLAYER":
		query.Set("cver", Clients["TV_EMBEDDED"].Version)
	case "WEB_EMBEDDED_PLAYER":
		query.Set("cver", Clients["WEB_EMBEDDED"].Version)
	}

	parsed_uri.RawQuery = query.Encode()

	return parsed_uri.String(), nil
}

func extractGlobalVariable(data string) (*FindVariableResult, error) {
	return FindVariable(string(data), FindVariableArgs{
		Includes: "-_w8_",
	})
}

func extractSigTimestamp(player_js string) (int, error) {
	matches := signatureTimestampRe.FindStringSubmatch(player_js)

	sig_timestamp, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return 0, err
	}

	return sig_timestamp, nil
}

func extractSigSourceCode(player_js string, g *FindVariableResult) (string, error) {
	matches := signatureSourceCodeRe.FindStringSubmatch(player_js)

	if len(matches) == 0 && g != nil && g.Name != "" {
		escaped_name := regexp.QuoteMeta(g.Name)
		lookup_regex_str := fmt.Sprintf(`function\(([A-Za-z_0-9]+)\)\{([A-Za-z_0-9]+=[A-Za-z_0-9]+\[%s\[\d+\]\]\([^)]*\)([\s\S]+?)\[%s\[\d+\]\]\([^)]*\))\}`, escaped_name, escaped_name)
		lookup_regex := regexp.MustCompile(lookup_regex_str)
		matches = lookup_regex.FindStringSubmatch(player_js)
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("failed to extract signature decipher algorithm")
	}

	var_name := string(matches[1])

	// Split on "." or "["
	splitParts := regexp.MustCompile(`[.\[]`).Split(matches[3], -1)
	var obj_name string

	if len(splitParts) > 0 {
		potential_obj_name := strings.TrimSpace(strings.ReplaceAll(splitParts[0], ";", ""))
		if regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`).MatchString(potential_obj_name) {
			obj_name = potential_obj_name
		} else {
			obj_name = potential_obj_name
			fmt.Printf("Warning: Potentially complex object name found: %s\n", obj_name)
		}
	}

	if obj_name == "" {
		return "", fmt.Errorf("could not determine object name from decipher logic: %s", matches[3])
	}

	re := regexp.MustCompile(fmt.Sprintf(`(?sm)var\s+\Q%s\E\s*=\s*\{(.*?)\}\s*;`, obj_name))
	obj_matches := re.FindStringSubmatch(player_js)

	if len(obj_matches) < 2 {
		re = regexp.MustCompile(fmt.Sprintf(`(?sm)\Q%s\E\s*=\s*\{(.*?)\}\s*;`, obj_name))
		obj_matches = re.FindStringSubmatch(player_js)
		if len(obj_matches) < 2 {
			return "", fmt.Errorf("object definition for '%s' not found", obj_name)
		}
	}

	functions := obj_matches[1]

	globalVarCode := g.Result
	if !strings.HasSuffix(strings.TrimSpace(globalVarCode), ";") {
		globalVarCode += ";"
	}

	decipherLogic := matches[2]

	return fmt.Sprintf("%s function descramble_sig(%s) { let %s={%s}; %s } descramble_sig(sig);", globalVarCode, var_name, obj_name, functions, decipherLogic), nil
}

func extractNSigSourceCode(data string, g *FindVariableResult) (name string, code string, err error) {
	nsig_function, err := FindFunction(string(data), FindFunctionArgs{
		Includes: fmt.Sprintf("new Date(%s", g.Name),
	})
	if err != nil {
		return
	}

	// For redundancy/the above fails:
	if nsig_function == nil {
		nsig_function, err = FindFunction(string(data), FindFunctionArgs{
			Includes: ".push(String.fromCharCode(",
		})
		if err != nil {
			return
		}
	}
	if nsig_function == nil {
		nsig_function, err = FindFunction(string(data), FindFunctionArgs{
			Includes: ".reverse().forEach(function",
		})
		if err != nil {
			return
		}
	}

	if nsig_function != nil {
		sc := fmt.Sprintf("%s; var %s", g.Result, nsig_function.Result)
		return nsig_function.Name, sc, nil
	}

	nsig_function, err = FindFunction(string(data), FindFunctionArgs{
		Includes: "-_w8_",
	})
	if err != nil {
		return
	}

	if nsig_function == nil {
		nsig_function, err = FindFunction(string(data), FindFunctionArgs{
			Includes: "1969",
		})
		if err != nil {
			return
		}
	}

	if nsig_function != nil {
		return nsig_function.Name, nsig_function.Result, nil
	}

	return
}

type FindVariableArgs struct {
	Name     string
	Includes string
	Regexp   string
}

type FindVariableResult struct {
	Start  int
	End    int
	Name   string
	Node   ast.Node
	Result string
}

func FindVariable(source string, args FindVariableArgs) (*FindVariableResult, error) {
	var reg *regexp.Regexp
	var err error

	if args.Regexp != "" {
		reg, err = regexp.Compile(args.Regexp)
		if err != nil {
			return nil, err
		}
	}

	program, err := parser.ParseFile(nil, "", source, 0)
	if err != nil {
		return nil, fmt.Errorf("error parsing JavaScript: %v", err)
	}

	var stack []ast.Statement
	stack = append(stack, program.Body...)

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		switch node := current.(type) {
		case *ast.ExpressionStatement:
			switch a := node.Expression.(type) {
			case *ast.CallExpression:
				switch a := a.Callee.(type) {
				case *ast.FunctionLiteral:
					for _, v := range a.DeclarationList {
						for _, va := range v.List {
							switch ab := va.Initializer.(type) {
							case *ast.CallExpression:
								c, ok := ab.Callee.(*ast.DotExpression)
								if !ok {
									continue
								}

								id, ok := va.Target.(*ast.Identifier)
								if !ok {
									continue
								}
								code, ok := c.Left.(*ast.StringLiteral)
								if !ok {
									continue
								}

								if (args.Includes != "" && strings.Index(code.Value.String(), args.Includes) > 0) || (args.Regexp != "" && reg.MatchString(code.Value.String())) {
									result := source[va.Idx0()-1 : va.Idx1()-1]
									return &FindVariableResult{
										Start:  int(va.Idx0()),
										End:    int(va.Idx1()),
										Name:   id.Name.String(),
										Node:   va,
										Result: result,
									}, nil
								}
							}
						}
					}
				}
			}
		}
	}
	return nil, nil
}

// FindFunctionArgs defines the search parameters
type FindFunctionArgs struct {
	Name     string
	Includes string
	Regexp   string
}

// FindFunctionResult holds the search result
type FindFunctionResult struct {
	Start  int
	End    int
	Name   string
	Node   ast.Node
	Result string
}

func FindFunction(source string, args FindFunctionArgs) (*FindFunctionResult, error) {
	var reg *regexp.Regexp
	var err error

	if args.Regexp != "" {
		reg, err = regexp.Compile(args.Regexp)
		if err != nil {
			return nil, err
		}
	}
	program, err := parser.ParseFile(nil, "", source, 0)
	if err != nil {
		return nil, fmt.Errorf("error parsing JavaScript: %v", err)
	}

	var stack []ast.Statement
	stack = append(stack, program.Body...)

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		switch node := current.(type) {
		case *ast.ExpressionStatement:
			switch a := node.Expression.(type) {
			case *ast.AssignExpression:
				id, ok := a.Left.(*ast.Identifier)
				if !ok {
					continue
				}

				_, ok = a.Right.(*ast.FunctionLiteral)
				if !ok {
					continue
				}

				code := source[a.Idx0():a.Idx1()]

				if (args.Name != "" && id.Name.String() == args.Name) ||
					(args.Includes != "" && strings.Index(code, args.Includes) > 0) || (args.Regexp != "" && reg.MatchString(code)) {
					result := source[a.Idx0()-1 : a.Idx1()-1]
					return &FindFunctionResult{
						Start:  int(a.Idx0()),
						End:    int(a.Idx1()),
						Name:   id.Name.String(),
						Node:   a,
						Result: result,
					}, nil
				}

			case *ast.CallExpression:
				switch a := a.Callee.(type) {
				case *ast.FunctionLiteral:
					stack = append(stack, a.Body.List...)
				}
			}
		}

		switch n := current.(type) {
		case *ast.BlockStatement:
			stack = append(stack, n.List...)
		}
	}

	return nil, nil
}
