// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

// --- source_uml and PR drafts: src/PresUml.rgr

func umlOf(files map[string]string) string {
	u := CreateNew_PresUml()
	for n, t := range files {
		u.add(n, t)
	}
	return u.mermaid(12, 0)
}

func hasAll(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("no %q in:\n%s", w, got)
		}
	}
}

func TestUmlLanguages(t *testing.T) {
	hasAll(t, umlOf(map[string]string{"a.ts": `
// class Fake {   in a comment
export abstract class Shape implements Drawable {
  private id: string = "{ not a brace";
  protected children: Shape[] = [];
  static count = 0;
  constructor(id: string) { this.id = id }
  abstract area(): number;
  draw(ctx: Ctx, scale = 1): void { if (x) { y() } }
  onClick = (e: Event) => { this.fire(e) }
}
export class Circle extends Shape { radius: number; area() { return 1 } }
interface Drawable { draw(ctx: Ctx): void }
enum Color { Red, Green = 2, Blue }
`}), "class Shape {\n        <<abstract>>", "-string id", "#Shape[] children", "+count$", "+area()* number", "+draw(ctx, scale)", "+onClick(e)",
		"<<interface>>", "<<enumeration>>\n        Red\n        Green\n        Blue", "Drawable <|.. Shape", "Shape <|-- Circle")
	if strings.Contains(umlOf(map[string]string{"a.ts": "// class Fake {\nconst s = \"class Nope {\"\n"}), "Fake") {
		t.Fatal("a class in a comment or string is not one")
	}

	hasAll(t, umlOf(map[string]string{"b.go": "package x\ntype Node struct {\n\tName string `json:\"name\"`\n\tchildren []*Node\n\tBase\n\tStore *Store\n}\n" +
		"type Store interface {\n\tGet(id string) (*Node, error)\n\tio.Closer\n}\nfunc (n *Node) Add(c *Node) error { return nil }\nfunc (n Node) size() int { return 0 }\n"}),
		"+string Name", "-Node[] children", "+Add(c) error", "-size() int", "+Get(id) (Node,error)", "Base <|-- Node", "Closer <|-- Store", "Node --> Store : Store")

	hasAll(t, umlOf(map[string]string{"c.py": "class Animal(ABC):\n    \"\"\"class Fake:\"\"\"\n    legs: int = 4\n    def __init__(self, name):\n        self.name = name\n        self._age = 0\n" +
		"    @abstractmethod\n    def speak(self) -> str:\n        pass\nclass Dog(Animal):\n    def speak(self):\n        return 'woof'\n"}),
		"<<abstract>>", "+int legs", "+name", "-_age", "+speak()* str", "Animal <|-- Dog")

	hasAll(t, umlOf(map[string]string{"d.java": "public class Repo<T> extends Base implements Store, Closeable {\n  private final Map<String, T> items = new HashMap<>();\n  public Repo(int a) {}\n  @Override\n  public List<T> findAll(String filter, int limit) { return null; }\n}\n"}),
		"-Map~String,T~ items", "+findAll(filter, limit) List~T~", "Base <|-- Repo", "Store <|.. Repo", "Closeable <|.. Repo")

	hasAll(t, umlOf(map[string]string{"e.rs": "pub struct Point { pub x: f64, y: Vec<Point> }\npub trait Shape: Debug { fn area(&self) -> f64; }\nimpl Shape for Point { fn area(&self) -> f64 { 0.0 } }\nimpl Point { pub fn new(x: f64) -> Self { Point{x} } }\n"}),
		"+f64 x", "-Vec~Point~ y", "+new(x) Self", "Shape <|.. Point", "Debug <|-- Shape")

	hasAll(t, umlOf(map[string]string{"f.rgr": "class A {\n    Extends(B)\n    def items:[C]\n    sfn make:A (n:int) {\n        if (x) { y }\n    }\n}\nclass C {\n    def name:string \"\"\n}\n"}),
		"+C[] items", "+make(n)$ A", "B <|-- A", "A --> \"*\" C : items")

	// a chain deeper than wide runs left to right on a wide slide
	hasAll(t, umlOf(map[string]string{"chain.ts": "class A { b: B }\nclass B { c: C }\nclass C {}\n"}), "classDiagram\n    direction LR\n")
	if strings.Contains(umlOf(map[string]string{"wide.ts": "class A {}\nclass B extends A {}\nclass C extends A {}\nclass D extends A {}\n"}), "direction") {
		t.Fatal("a wide tree stays top-down")
	}

	hasAll(t, umlOf(map[string]string{"g.kt": "data class User(val id: Int, var name: String) : Entity(), Serializable\n"}), "+Int id", "+String name", "Entity <|-- User", "Serializable <|.. User")
	hasAll(t, umlOf(map[string]string{"h.cs": "public class Svc : BaseSvc, IDisposable { public int Count { get; set; } private List<Item> items; public void Run(int x) {} }"}),
		"+int Count", "-List~Item~ items", "+Run(x)", "BaseSvc <|-- Svc", "IDisposable <|.. Svc")
}

// a big codebase: the most connected classes, a few members each
func TestUmlLimits(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("class C" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + " { a: number; b: number; c: number; d: number; e: number; f(){} g(){} h(){} i(){} j(){} }\n")
	}
	b.WriteString("class Hub { x: CAa; y: CBa }\n")
	u := CreateNew_PresUml()
	u.add("big.ts", b.String())
	m := u.mermaid(8, 0)
	eq(t, strings.Count(m, "    class "), 8)
	hasAll(t, m, "class Hub", "Hub --> CAa : x")
	eq(t, u.leftOut(8), int64(23))
	if strings.Count(m, "        +a") > 8 || strings.Contains(m, "+e") {
		t.Fatal("eight classes get three members of each kind:\n" + m)
	}
	if u.add("README.md", "# x") != 0 || len(u.skipped) != 1 {
		t.Fatal("a file in no language it reads is skipped")
	}
}

// source_uml with files as text; the slide it returns makes a deck without
// warnings about its diagram
func TestSourceUmlTool(t *testing.T) {
	s := start(t, testEnv(nil, nil), "")
	defer s.close()
	r := call(t, s, "source_uml", map[string]any{"files": []any{
		map[string]any{"name": "shape.ts", "text": "export class Shape { area(): number { return 0 } }\nexport class Square extends Shape { side: number; corners: Point[] }\nclass Point { x: number; y: number }\n"},
		map[string]any{"name": "notes.txt", "text": "class Nope {}"},
	}})
	if r.IsError {
		t.Fatal(textOf(r))
	}
	o := sc(r)
	hasAll(t, o["mermaid"].(string), "classDiagram", "Shape <|-- Square")
	eq(t, len(list(o["classes"])), 3)
	match(t, textOf(r), `^3 classes in 1 files; the diagram shows 3\.`)
	md := o["markdown"].(string)
	f := fakeFirebase()
	s2 := start(t, withSignIn(testEnv(&f, nil)), signIn(f))
	defer s2.close()
	// members' types read on a dark theme's glass and a light one's
	for _, th := range []string{"aurora", "corporate"} {
		c := call(t, s2, "create_presentation", map[string]any{"title": "Classes", "theme": th, "markdown": "# Code\n\n" + md + "\n"})
		if c.IsError {
			t.Fatal(textOf(c))
		}
		for _, w := range list(sc(c)["warnings"]) {
			t.Fatal(th, w)
		}
	}
	match(t, textOf(call(t, s, "source_uml", map[string]any{})), `give github`)
	match(t, textOf(call(t, s, "source_uml", map[string]any{"files": []any{map[string]any{"name": "a.md", "text": "x"}}})), `No classes were found in 0 source files \(1 files in languages`)
	match(t, textOf(call(t, s, "source_uml", map[string]any{"github": "../etc"})), `^Invalid arguments`)
}

func TestGitHubSrcRefs(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r":                     "https://api.github.com/repos/o/r/contents/",
		"https://github.com/o/r/tree/main/src/lib":   "https://api.github.com/repos/o/r/contents/src/lib?ref=main",
		"https://github.com/o/r/blob/v2/src/a.ts#L3": "https://api.github.com/repos/o/r/contents/src/a.ts?ref=v2",
		"o/r.git/src": "https://api.github.com/repos/o/r/contents/src",
	} {
		g := GhSrc_static_parse(in)
		eq(t, g.err, "", in)
		eq(t, g.contents(g.path), want, in)
	}
	for _, bad := range []string{"o", "o/r/a b", "o/r/../x", "https://github.com/../r"} {
		if GhSrc_static_parse(bad).err == "" {
			t.Fatal("accepted", bad)
		}
	}
}
