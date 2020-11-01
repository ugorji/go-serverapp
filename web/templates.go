/*
TEMPLATES

Templates support optimized management of templates for the whole application.

The model is as below:
  - Each view page (e.g. landing page) corresponds to a given Template (set)
  - A config file defines the templates, so we can re-use templates
  - A Tree is used, so that a TemplateSet can be easily configured to share templates. E.g.
    All the core templates share the same header and footers.

Typical usage for loading from within main app
  vcn = new(web.ViewConfigNode)
  myVfs = new(util.VFS)
  err = myVfs.AddIfExist("templates.zip", "templates")
  f, err = os.Open("resources/core/web/views.json")
  err = json.NewDecoder(f).Decode(vcn)
  views = web.NewViews()
  views.FnMap["Eq"] = reflect.DeepEqual // MUST register functions BEFORE adding templates
  views.AddTemplates(myVfs, regexp.MustCompile(`.*\.thtml`)) // ...
  views.Load(vcn)

The View Handlers can call:
  views.Views["core.landing"].Execute(writer, "main", data)
*/
package web

// NOTE: Understand how templates work
//   - Functions have to be registered before templates are parsed
//   - After templates are parsed, then putting functions into the Set has no meaning
//   - However, since we only share the ParseTree, we have to put the functions back
//     on the TemplateSet for each one.
//
// Aliases
// When an alias is defined, the ViewNode uses the alias'es Parent and Views to
// Populate it map of views.

import (
	"text/template"
	"text/template/parse"

	"fmt"
	"io/ioutil"
	"regexp"
	"strings"

	"github.com/ugorji/go-common/errorutil"
	"github.com/ugorji/go-common/vfs"
)

// Manage all the template sets for the application.
// Web Handlers should call TemplateSet.Execute(...) to render their content.
// Note that we really share templates (ie by sharing the actual parse trees).
type Views struct {
	FnMap template.FuncMap
	Views map[string]*template.Template
	tmpls map[string]*parse.Tree
}

// A node that is parseable from an external source, that defines a tree of
// configured views. Each view is eventually used to construct a template.Set
type ViewConfigNode struct {
	Name     string
	AliasTo  string
	Parent   *ViewConfigNode
	Children []*ViewConfigNode
	Views    map[string]string
}

func NewViews() *Views {
	return &Views{
		FnMap: make(template.FuncMap),
		Views: make(map[string]*template.Template),
		tmpls: make(map[string]*parse.Tree),
	}
}

// An app can call this function multiple times passing a VFS and a regexp. It will Load
// all the templates matching that regexp, and parse them.
// (We need to add funcMap so that we can parse/find functions defined at parse time, etc).
func (views *Views) AddTemplates(vfs *vfs.Vfs, r *regexp.Regexp) (err error) {
	defer errorutil.OnError(&err)
	errm := make(errorutil.Multi, 0, 4)
	ls := vfs.Matches(r, nil, true)
	log.Debug(nil, "LT: Matches: %v", ls)
	for _, s := range ls {
		rc, err := vfs.Find(s)
		if err != nil {
			errm = append(errm, err)
			continue
		}
		defer rc.Close()
		bs, err := ioutil.ReadAll(rc)
		if err != nil {
			errm = append(errm, err)
			continue
		}
		//log.Debug(nil, "LT: Loading template: %v, from: %v", s, tmplstrs[s])
		t, err := template.New(s).Funcs(views.FnMap).Parse(string(bs))
		if err != nil {
			errm = append(errm, err)
			continue
		}
		views.tmpls[s] = t.Tree
	}
	//println("0.XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX", "len", len(errm))
	if len(errm) == 0 {
		return nil
	}
	return errm
}

// This is called after templates have been found. Using the map, it will
// create TemplateSets for each view (sharing templates performantly)
func (views *Views) Load(vcn *ViewConfigNode) (err error) {
	defer errorutil.OnError(&err)
	vcfg := vcn.nodeToMap()
	errm := make(errorutil.Multi, 0, 4)
	for k, v := range vcfg {
		tset := template.New(k).Funcs(views.FnMap)
		for k2, v2 := range v {
			if v2 == "" || v2 == "-" {
				continue
			}
			t0, ok := views.tmpls[v2]
			if !ok {
				errm = append(errm, fmt.Errorf("No Template found for key: %v", v2))
				continue
			}
			tset.AddParseTree(k2, t0)
			//log.Debug(nil, "TSET: Parsing: Set: %v, text: %v", k, tmpls[v2])
			//_, err3 := t.ParseInSet(tmpls[v2], tset)
			//if err3 != nil { errm = append(errm, err3); continue }
		}
		log.Debug(nil, "TemplateSet: view: %v, tset: %v", k, tset)
		views.Views[k] = tset
	}
	if len(errm) == 0 {
		return nil
	}
	return errm
}

// Calls RehashVcn on the ViewConfigNode, and then uses it to
// creates and populates a map usable in the LoadTemplateSets
func (n *ViewConfigNode) nodeToMap() map[string]map[string]string {
	n.rehashVcn()
	log.Debug(nil, "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@")
	log.Debug(nil, "===> VCN: %#v", n)
	vcfg := make(map[string]map[string]string)
	// do 2 passes, so first time, we ensure we have info for all parents, and second time, we
	// get info for all aliases.
	vcnToMap(false, n, n, vcfg)
	vcnToMap(true, n, n, vcfg)
	return vcfg
}

func vcnToMapFnWork(nn *ViewConfigNode, n *ViewConfigNode, vcfg map[string]map[string]string) {
	m2 := make(map[string]string)
	if nn.Parent != nil {
		for k2, v2 := range vcfg[nn.Parent.Name] {
			m2[k2] = v2
		}
	}
	for k2, v2 := range nn.Views {
		m2[k2] = v2
	}
	vcfg[n.Name] = m2
}

// populates a map usable in the LoadTemplateSets
// vcfg is a {ViewName: {TemplateName: TemplateSource} }
func vcnToMap(doAliases bool, n *ViewConfigNode, root *ViewConfigNode,
	vcfg map[string]map[string]string) {
	if doAliases && n.AliasTo != "" {
		nreal := n
		if n2 := root.findVcnByName(n.AliasTo); n2 != nil {
			nreal = n2
		}
		vcnToMapFnWork(nreal, n, vcfg)
	} else if !doAliases && n.AliasTo == "" {
		vcnToMapFnWork(n, n, vcfg)
	}

	for _, n2 := range n.Children {
		vcnToMap(doAliases, n2, root, vcfg)
	}
}

//Update the parents of its child nodes.
//If no content is defined for a view, default to:
//   the Name (with . replaced by /) plus .thtml.
//For example, view for core.landing, if not defined, defaults to
//   core/landing.thtml.
func (n *ViewConfigNode) rehashVcn() {
	if n.Views == nil {
		n.Views = make(map[string]string)
	}
	if _, ok := n.Views["content"]; !ok {
		n.Views["content"] = strings.Replace(n.Name, ".", "/", -1) + ".thtml"
	}
	for _, n2 := range n.Children {
		n2.Parent = n
		n2.rehashVcn()
	}
}

func (n *ViewConfigNode) findVcnByName(vname string) *ViewConfigNode {
	if n.Name == vname {
		return n
	}
	for _, n2 := range n.Children {
		if n3 := n2.findVcnByName(vname); n3 != nil {
			return n3
		}
	}
	return nil
}

/*
func resolveAliases(n *ViewConfigNode, root *ViewConfigNode) {
	//if an alias, it has no children (ie it inherits its children)
	if n.AliasTo != "" {
		n2 := findVcnByName(root, n.AliasTo)
		log.Debug(nil, "findVcnByName: name: %v, ----> N: %#v, ----> N2: %#v", n.AliasTo, n, n2)
		if n2 != nil {
			log.Debug(nil, "resolveAliases (BEFORE): n: %#v", n)
			n3 := *n2
			n3.AliasTo, n3.Name = n.AliasTo, n.Name
			*n = n3
			log.Debug(nil, "resolveAliases (AFTER): n: %#v", n)
		}
	} else {
		for _, n2 := range n.Children {
			resolveAliases(n2, root)
		}
	}
}
*/
