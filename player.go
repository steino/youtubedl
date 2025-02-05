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

	"github.com/corpix/uarand"
	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/patrickmn/go-cache"
)

type Player struct {
	httpClient    *http.Client
	sig_timestamp int
	sig_sc        string
	nsig_sc       string
	nsig_name     string
	nsig_check    string
	visitorData   string
}

var (
	playerRe              = regexp.MustCompile(`(?m)player\\\/(\w+)\\/`)
	signatureTimestampRe  = regexp.MustCompile(`(?m)signatureTimestamp:(\d+),`)
	signatureSourceCodeRe = regexp.MustCompile(`(?m)function\(([A-Za-z_0-9]+)\)\{([A-Za-z_0-9]+=[A-Za-z_0-9]+\.split\(""\)(.+?)\.join\(""\))\}`)
	nsigCheckRe           = regexp.MustCompile(`(?m)if\(typeof (.+)==="undefined"\)return`)

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
	req.Header.Add("User-Agent", uarand.GetRandom())

	player_resp, err := player.httpClient.Do(req)
	if err != nil {
		return
	}

	player_js, err := io.ReadAll(player_resp.Body)
	if err != nil {
		return
	}

	player.sig_timestamp, err = extractSigTimestamp(string(player_js))
	if err != nil {
		return
	}

	player.sig_sc, err = extractSigSourceCode(string(player_js))
	if err != nil {
		return
	}

	player.nsig_name, player.nsig_sc, err = extractNSigSourceCode(string(player_js))
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
			return "", nil
		}

		s := query.Get("s")
		vm := goja.New()
		vm.Set("sig", s)
		sig, err := vm.RunString(p.sig_sc)
		if err != nil {
			return "", nil
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
	if p.nsig_sc != "" && p.nsig_check != "" && n != "" {
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

			nsig = decipher(query.Get("n"))
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

func extractSigTimestamp(player_js string) (int, error) {
	matches := signatureTimestampRe.FindStringSubmatch(player_js)

	sig_timestamp, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return 0, err
	}

	return sig_timestamp, nil
}

func extractSigSourceCode(player_js string) (string, error) {
	matches := signatureSourceCodeRe.FindStringSubmatch(player_js)

	var_name := string(matches[1])

	// Split on "." or "["
	splitParts := regexp.MustCompile(`[.\[]`).Split(matches[3], -1)
	var obj_name string

	if len(splitParts) > 0 {
		obj_name = strings.TrimSpace(strings.ReplaceAll(splitParts[0], ";", ""))
	}
	re := regexp.MustCompile(fmt.Sprintf(`(?sm)var %s={(.*?)}\;`, obj_name))
	if !re.MatchString(player_js) {
		return "", fmt.Errorf("object %s not found", obj_name)
	}

	functions := re.FindStringSubmatch(player_js)[1]

	return fmt.Sprintf("function descramble_sig(%s) { let %s={%s}; %s} descramble_sig(sig);", var_name, obj_name, functions, matches[2]), nil
}

func extractNSigSourceCode(data string) (name string, code string, err error) {
	nsig_function, err := FindFunction(string(data), FindFunctionArgs{
		Includes: "enhanced_except",
	})
	if err != nil {
		return
	}
	if nsig_function == nil {
		nsig_function, err = FindFunction(string(data), FindFunctionArgs{
			Includes: "-_w8_",
		})
		if err != nil {
			return
		}
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
