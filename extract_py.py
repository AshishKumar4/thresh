"""thresh's Python extractor: reads one Python source on stdin with the stdlib `ast` and writes its facts as
JSON to stdout. Embedded in the binary (extract_py.go) and run by the target's own python3; the target
need not import or run.

    {"ok": bool,
     "funcs": [{"name": "Class.method", "line": n}, ...],          every function and method, qualified
     "uses": [{"name": "a.b.c", "line": n, "func": "Class.method"}, ...],
         every dotted name referenced whose root is imported (resolved through the import, in the scope the
         name is read in: a parameter or local of the same name shadows it) or bound nowhere in the file, with
         the function it runs in ("" for module level and a module-level class body: a class body runs where
         the class is defined, a default or a decorator where its function is; a function's annotations are
         its own);
         an absolute import statement is a use of each name it imports
     "registries": {"NAME": [{"key": "k", "line": n}, ...], ...},  (named false: a computed key)
         each module-level dict, set, list or tuple literal (or dict(...) call) bound to a name, and each
         module-level `NAME[k] = ...`: its keys or elements, constants, dotted names and tuples of them
         ("(a, b)")
     "normalized": "..."}                                            the module's AST without docstrings

"ok" is false, the rest empty, where the source does not parse.

Run with the argument `copies`, it reads a JSON list of sources instead and writes, for each, whether it
parsed and its functions as a copy is read:

    {"ok": bool, "funcs": [{"name": "Class.method", "line": n, "digest": "...", "prints": [n, ...]}, ...]}

each module-level function and method of at least MIN_NODES syntax nodes, its body as tokens (every
identifier one token, every constant its type, docstrings, imports, stubs and bare annotations aside), the
digest of those tokens, and their winnowed fingerprints: each run of K tokens hashed, the least hash of
each W runs in a row kept, so a copy with a statement changed, added or dropped keeps most of them.
"""
import ast
import hashlib
import json
import sys
from itertools import pairwise

MIN_NODES = 60
"""A function of fewer syntax nodes is too short to call a copy: its steps are an idiom."""
K, W = 30, 16
"""The fingerprints: each K tokens in a row hashed, the least hash of each W hashes in a row kept."""
DROPPED = {"ctx", "type_comment", "decorator_list", "returns", "annotation", "type_params", "kind"}
"""Fields that hold no step: how a name is used, comments, decorators, annotations."""
BODIES = ("body", "orelse", "finalbody")
MODULUS, BASE = (1 << 61) - 1, 1_000_003


def chain(node):
    """The root name and the attributes an expression of names and attributes spells; None for anything else
    (a call's result, a subscript)."""
    parts = []
    while isinstance(node, ast.Attribute):
        parts.append(node.attr)
        node = node.value
    if not isinstance(node, ast.Name):
        return None
    return node.id, list(reversed(parts))


def dotted(node):
    """The dotted name an expression of names and attributes spells, as written; None for anything else."""
    spelled = chain(node)
    return None if spelled is None else ".".join([spelled[0], *spelled[1]])


UNKNOWN = object()
"""A name bound to something that is not an absolute import: a parameter, a local, a relative import."""


def bound(node):
    """Each (local name, what it is bound to) an import statement binds: an absolute import's dotted name, or
    UNKNOWN for a relative one."""
    if isinstance(node, ast.Import):
        for alias in node.names:
            if alias.asname:
                yield alias.asname, alias.name
            else:
                yield alias.name.split(".")[0], alias.name.split(".")[0]
    elif isinstance(node, ast.ImportFrom):
        for alias in node.names:
            if alias.name != "*":
                absolute = node.module and not node.level
                yield alias.asname or alias.name, f"{node.module}.{alias.name}" if absolute else UNKNOWN


def imported(node):
    """Each dotted name an absolute import statement imports (a star import, its module)."""
    if isinstance(node, ast.Import):
        yield from (alias.name for alias in node.names)
    elif isinstance(node, ast.ImportFrom) and node.module and not node.level:
        for alias in node.names:
            yield node.module if alias.name == "*" else f"{node.module}.{alias.name}"


MATCHED = tuple(getattr(ast, name) for name in ("MatchAs", "MatchStar", "MatchMapping") if hasattr(ast, name))
"""The patterns that bind a name (Python 3.10 on)."""
SCOPES = (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Lambda, ast.ListComp, ast.SetComp,
          ast.DictComp, ast.GeneratorExp)


def arguments(args):
    return [arg.arg for arg in (*args.posonlyargs, *args.args, *args.kwonlyargs) if arg] + \
        [arg.arg for arg in (args.vararg, args.kwarg) if arg]


GLOBAL = object()
"""A name a function declares `global`: read from the module, whatever the scopes between bind."""


def walked(body, skipped=frozenset()):
    """The nodes of a scope's statements `body`, nested scopes' insides aside (their nodes themselves kept), and
    the insides of the nodes whose ids are `skipped`."""
    pending = list(body)
    while pending:
        node = pending.pop()
        yield node
        if not isinstance(node, SCOPES) and id(node) not in skipped:
            pending.extend(ast.iter_child_nodes(node))


MISSING = {"ImportError", "ModuleNotFoundError"}
"""The exceptions an optional import's fallback catches."""


def fallbacks(body):
    """The `except` handlers of a scope's statements `body` that are optional imports' fallbacks, each with the
    names its `try` imports: each catches ImportError or ModuleNotFoundError, of a `try` whose body imports."""
    for node in walked(body):
        if isinstance(node, ast.Try):
            protected = {local for statement in walked(node.body)
                         if isinstance(statement, (ast.Import, ast.ImportFrom)) for local, _ in bound(statement)}
            for handler in node.handlers if protected else ():
                caught = handler.type.elts if isinstance(handler.type, ast.Tuple) else [handler.type]
                if any(isinstance(kind, ast.Name) and kind.id in MISSING for kind in caught):
                    yield handler, protected


def bindings(body, names=(), skipped=frozenset(), module=False):
    """What each name a scope binds is bound to (its statements `body`, its parameters `names`): an import's
    dotted name where every binding of it is the same absolute import, else UNKNOWN; GLOBAL for a name it
    declares `global`. The names of the functions and classes it defines are bound; a name it declares
    `nonlocal` is left to the scope that binds it; a module's `global` declarations bind nothing. The bindings
    inside the nodes whose ids are `skipped` are not read."""
    found, declared, outer = {name: {UNKNOWN} for name in names}, set(), set()
    for node in walked(body, skipped):
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            for local, value in bound(node):
                found.setdefault(local, set()).add(value)
        elif isinstance(node, ast.Global):
            declared.update(() if module else node.names)
        elif isinstance(node, ast.Nonlocal):
            outer.update(node.names)
        elif isinstance(node, ast.Name) and not isinstance(node.ctx, ast.Load):
            found.setdefault(node.id, set()).add(UNKNOWN)
        elif isinstance(node, ast.ExceptHandler) and node.name:
            found.setdefault(node.name, set()).add(UNKNOWN)
        elif isinstance(node, MATCHED) and getattr(node, "name", None) or getattr(node, "rest", None):
            found.setdefault(getattr(node, "name", None) or node.rest, set()).add(UNKNOWN)
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            found.setdefault(node.name, set()).add(UNKNOWN)
    named = {name: values.pop() if len(values) == 1 else UNKNOWN
             for name, values in found.items() if name not in outer | declared}
    return {**named, **{name: GLOBAL for name in declared}}


def module_bindings(body, module=False):
    """A module's (or a class body's) bindings: as `bindings`, but a name bound by one absolute import alone,
    the optional imports' fallbacks aside (`try: import a` / `except ImportError: a = None`: the handler's
    bindings of a name its `try` imports), and by no other import in them, is that import. A module's `global`
    declarations bind nothing."""
    handlers = list(fallbacks(body))
    everywhere = bindings(body, module=module)
    outside = bindings(body, skipped=frozenset(id(handler) for handler, _ in handlers), module=module)
    others = {}
    for handler, protected in handlers:
        for name, value in bindings(handler.body).items():
            if name not in protected:
                outside[name] = UNKNOWN  # a handler's binding of another name is no fallback of it
            elif value is not UNKNOWN:
                others.setdefault(name, set()).add(value)
    imported = {name: value for name, value in outside.items() if value is not UNKNOWN and value is not GLOBAL
                and others.get(name, {value}) == {value}}
    named = {name: UNKNOWN if value is not GLOBAL else GLOBAL for name, value in everywhere.items()}
    return {**named, **imported}


def key_of(node, within_call=False):
    """A registry key or element: a string or number constant's value, None as "None", a dotted name, a tuple
    of them as "(a, b)", or a call of a dotted name with them as its arguments, an object constructed from its
    parts, as "Kind('a', made=Made.FORWARD)": positional arguments, then keywords as written, a string among
    them quoted so its commas never read as another argument's; None for anything else, a key computed by the
    program."""
    if isinstance(node, ast.Constant) and (node.value is None or isinstance(node.value, (str, int))):
        return repr(node.value) if within_call and isinstance(node.value, str) else str(node.value)
    if isinstance(node, ast.Tuple):
        parts = [key_of(element, within_call) for element in node.elts]
        return None if None in parts else "(" + ", ".join(parts) + ")"
    if isinstance(node, ast.Call) and (callee := dotted(node.func)) is not None:
        parts = [key_of(argument, True) for argument in node.args]
        named = [(keyword.arg, key_of(keyword.value, True)) for keyword in node.keywords]
        if None in parts or any(name is None or value is None for name, value in named):
            return None
        return f"{callee}(" + ", ".join([*parts, *(f"{name}={value}" for name, value in named)]) + ")"
    return dotted(node)


def constructed(node):
    """Whether a registry key holds an object's construction (a call), which no string key is, though one
    may read as it."""
    return any(isinstance(part, ast.Call) for part in ast.walk(node))


def entries(value):
    """The keys or elements of a registry literal: a dict, set, list or tuple display, or dict(...) by
    keywords, through frozenset(...), set(...), tuple(...) or MappingProxyType(...) around one."""
    while (isinstance(value, ast.Call) and isinstance(value.func, (ast.Name, ast.Attribute))
           and dotted(value.func) in ("frozenset", "set", "tuple", "list", "MappingProxyType",
                                          "types.MappingProxyType") and len(value.args) == 1):
        value = value.args[0]
    if isinstance(value, ast.Dict):
        return [(key, key.lineno) for key in value.keys if key is not None]
    if isinstance(value, (ast.Set, ast.List, ast.Tuple)):
        return [(element, element.lineno) for element in value.elts]
    if isinstance(value, ast.Call) and dotted(value.func) == "dict" and not value.args:
        return [(ast.Constant(keyword.arg), keyword.value.lineno) for keyword in value.keywords if keyword.arg]
    return None


def undocumented(tree):
    """`tree` without its docstrings: the first statement of a module, class or function when it is a string."""
    for node in ast.walk(tree):
        if isinstance(node, (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            body = node.body
            if body and isinstance(body[0], ast.Expr) and isinstance(body[0].value, ast.Constant) \
                    and isinstance(body[0].value.value, str):
                node.body = body[1:] or [ast.Pass()]
    return tree


class Facts(ast.NodeVisitor):
    """The facts of one module. `frames` are the scopes a name is read in, innermost last, each with its kind
    ("module", "class", "function") and bindings; `qualified` names the definitions the visit is inside, for
    a function's name; `running` is the qualified name of each function the visit is inside, for a use's."""

    def __init__(self):
        self.funcs, self.uses, self.registries = [], [], {}
        self.frames, self.qualified, self.running = [], [], []

    def resolve(self, name):
        """The dotted name `name` is bound to where it is read; None where it is bound to something else. A
        class body's names are seen only from that body, not from the functions defined in it."""
        for at, (kind, names) in enumerate(reversed(self.frames)):
            if kind == "class" and at > 0:
                continue
            if names.get(name) is GLOBAL:
                names = self.frames[0][1]
                return name if name not in names else None if names[name] is UNKNOWN else names[name]
            if name in names:
                return None if names[name] is UNKNOWN else names[name]
        return name

    def within(self, kind, names, body):
        self.frames.append((kind, names))
        for node in body:
            self.visit(node)
        self.frames.pop()

    def visit_Module(self, node):
        self.within("module", module_bindings(node.body, module=True), node.body)

    def definition(self, node):
        """What a function's or a class's definition evaluates where it is defined: its decorators, and a
        function's defaults, a class's bases and keywords."""
        for decorator in node.decorator_list:
            self.visit(decorator)
        if isinstance(node, ast.ClassDef):
            for expression in (*node.bases, *(keyword.value for keyword in node.keywords)):
                self.visit(expression)
            return
        args = node.args
        for expression in (*args.defaults, *(default for default in args.kw_defaults if default)):
            self.visit(expression)

    def signature(self, node):
        """A function's annotations: its signature's, so its own uses, though read where it is defined (a
        parameter does not shadow them); deferred, they run nowhere until read."""
        args = node.args
        for arg in (*args.posonlyargs, *args.args, *args.kwonlyargs, args.vararg, args.kwarg):
            if arg is not None and arg.annotation is not None:
                self.visit(arg.annotation)
        if node.returns:
            self.visit(node.returns)

    def visit_ClassDef(self, node):
        self.definition(node)
        self.qualified.append(node.name)
        self.within("class", module_bindings(node.body), node.body)
        self.qualified.pop()

    def visit_FunctionDef(self, node):
        name = ".".join([*self.qualified, node.name])
        self.funcs.append({"name": name, "line": node.lineno})
        self.definition(node)
        self.qualified.append(node.name)
        self.running.append(name)
        self.signature(node)
        self.within("function", bindings(node.body, arguments(node.args)), node.body)
        self.running.pop()
        self.qualified.pop()

    visit_AsyncFunctionDef = visit_FunctionDef

    def visit_Lambda(self, node):
        for expression in (*node.args.defaults, *(default for default in node.args.kw_defaults if default)):
            self.visit(expression)
        self.within("function", {name: UNKNOWN for name in arguments(node.args)}, [node.body])

    def comprehension(self, node, parts):
        """A comprehension's scope: its first iterable is read outside it, the rest inside, where its targets
        are bound."""
        first, *rest = node.generators
        self.visit(first.iter)
        targets = {target.id: UNKNOWN for generator in node.generators
                   for target in ast.walk(generator.target) if isinstance(target, ast.Name)}
        inside = [first.target, *first.ifs, *(part for generator in rest
                                              for part in (generator.target, generator.iter, *generator.ifs))]
        self.within("function", targets, [*inside, *parts])

    def visit_ListComp(self, node):
        self.comprehension(node, [node.elt])

    visit_SetComp = visit_GeneratorExp = visit_ListComp

    def visit_DictComp(self, node):
        self.comprehension(node, [node.key, node.value])

    def use(self, name, line):
        self.uses.append({"name": name, "line": line, "func": self.running[-1] if self.running else ""})

    def visit_Import(self, node):
        for name in imported(node):
            self.use(name, node.lineno)

    visit_ImportFrom = visit_Import

    def visit_Attribute(self, node):
        spelled = chain(node)
        if spelled is None:
            self.generic_visit(node)
        elif (root := self.resolve(spelled[0])) is not None:
            self.use(".".join([root, *spelled[1]]), node.lineno)

    def visit_Name(self, node):
        if isinstance(node.ctx, ast.Load) and (root := self.resolve(node.id)) is not None:
            self.use(root, node.lineno)

    def registry(self, name, found):
        """Each entry of a registry literal, by its key's text and whether it is constructed; one whose key is
        computed is listed unnamed, never dropped, so a set holding it is seen to be unread there."""
        if found is not None:
            listed = self.registries.setdefault(name, [])
            for key, line in found:
                text = key_of(key)
                listed.append({"key": text or "", "named": text is not None, "constructed": constructed(key),
                               "line": line})

    def visit_Assign(self, node):
        if len(self.frames) == 1:
            for target in node.targets:
                if isinstance(target, ast.Name):
                    self.registry(target.id, entries(node.value))
                elif isinstance(target, ast.Subscript) and isinstance(target.value, ast.Name):
                    self.registry(target.value.id, [(target.slice, node.lineno)])
        self.generic_visit(node)

    def visit_AnnAssign(self, node):
        if len(self.frames) == 1 and isinstance(node.target, ast.Name) and node.value is not None:
            self.registry(node.target.id, entries(node.value))
        self.generic_visit(node)


def docstring(statement):
    return (isinstance(statement, ast.Expr) and isinstance(statement.value, ast.Constant)
            and isinstance(statement.value.value, str))


def declaration(statement, in_class):
    """A docstring, an import, a stub (a function whose body is only `...` or `pass`), a bare annotation,
    or a class's annotated field: no step."""
    if isinstance(statement, (ast.FunctionDef, ast.AsyncFunctionDef)):
        body = [line for line in statement.body if not docstring(line)]
        return len(body) <= 1 and all(
            isinstance(line, ast.Pass) or (isinstance(line, ast.Expr) and isinstance(line.value, ast.Constant)
                                           and line.value.value is Ellipsis) for line in body)
    if isinstance(statement, ast.AnnAssign):
        return in_class or statement.value is None
    return docstring(statement) or isinstance(statement, (ast.Import, ast.ImportFrom))


def plain(statement):
    """A valued annotated assignment as the assignment it is."""
    if isinstance(statement, ast.AnnAssign) and statement.value is not None:
        return ast.copy_location(ast.Assign(targets=[statement.target], value=statement.value), statement)
    return statement


def callee(statement):
    """The function a statement that is only a call, or only names a call's result, calls, as written."""
    if isinstance(statement, (ast.Expr, ast.Assign)) and isinstance(statement.value, ast.Call):
        return ast.dump(statement.value.func)
    return None


def stepped(tree):
    """`tree` rewritten in place to its steps: declarations dropped, valued annotations plain. Returns the
    ids of the statements in runs of calls to one function, which declare data: their constants are read
    as their values, so two tables of calls are not one copy."""
    data = set()
    for node in ast.walk(tree):
        for field in BODIES:
            statements = getattr(node, field, None)
            if not (isinstance(statements, list) and statements and isinstance(statements[0], ast.stmt)):
                continue
            kept = [plain(statement) for statement in statements
                    if not declaration(statement, isinstance(node, ast.ClassDef))]
            setattr(node, field, kept)
            for first, second in pairwise(kept):
                if callee(first) is not None and callee(first) == callee(second):
                    data.update((id(first), id(second)))
    return data


def tokens(node, out, data, declaring):
    """`node` as tokens into `out`: its type and fields, every identifier "ID", every constant its type (None,
    booleans and the ellipsis as themselves), or its value within a statement that declares data (its id in
    `data`, or `declaring`). Returns its count of syntax nodes."""
    out.append(type(node).__name__)
    declaring = declaring or id(node) in data
    count = 1
    for field, value in ast.iter_fields(node):
        if field in DROPPED:
            continue
        out.append(field)
        if isinstance(node, ast.Constant) and field == "value":
            literal = declaring or value is None or value is Ellipsis or isinstance(value, bool)
            out.append(repr(value) if literal else type(value).__name__)
        elif isinstance(value, ast.AST):
            count += tokens(value, out, data, declaring)
        elif isinstance(value, list):
            out.append(f"[{len(value)}")
            for item in value:
                if isinstance(item, ast.AST):
                    count += tokens(item, out, data, declaring)
                else:
                    out.append("ID" if isinstance(item, str) else repr(item))
        else:
            out.append("ID" if isinstance(value, str) else repr(value))
    return count


def fingerprints(stream):
    """The least hash of each W in a row of the hashes of each K tokens in a row (all of them, under W)."""
    codes = [int.from_bytes(hashlib.blake2b(token.encode(), digest_size=8).digest(), "big") % MODULUS
             for token in stream]
    if len(codes) < K:
        return []
    top = pow(BASE, K - 1, MODULUS)
    grams, value = [], 0
    for at, code in enumerate(codes):
        if at >= K:
            value = (value - codes[at - K] * top) % MODULUS
        value = (value * BASE + code) % MODULUS
        if at >= K - 1:
            grams.append(value)
    return sorted({min(grams[start:start + W]) for start in range(max(len(grams) - W + 1, 1))})


def copies(source):
    """Whether `source` parses, and each module-level function and method of at least MIN_NODES nodes, as a
    copy is read."""
    try:
        tree = ast.parse(source)
    except (SyntaxError, ValueError):
        return {"ok": False, "funcs": []}
    data = stepped(tree)
    found = []

    def visit(node, scope, depth):
        for child in ast.iter_child_nodes(node):
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef)):
                stream, nodes = [], 0
                for statement in child.body:
                    nodes += tokens(statement, stream, data, False)
                    stream.append(";")
                if nodes >= MIN_NODES:
                    found.append({"name": ".".join([*scope, child.name]), "line": child.lineno,
                                  "digest": hashlib.sha256("\x1f".join(stream).encode()).hexdigest(),
                                  "prints": fingerprints(stream)})
            elif isinstance(child, ast.ClassDef) and depth < 2:
                visit(child, [*scope, child.name], depth + 1)

    visit(tree, [], 0)
    return {"ok": True, "funcs": found}


def main():
    if sys.argv[1:] == ["copies"]:
        json.dump([copies(source) for source in json.load(sys.stdin)], sys.stdout)
        return
    source = sys.stdin.read()
    try:
        tree = ast.parse(source)
    except (SyntaxError, ValueError):
        json.dump({"ok": False, "funcs": [], "uses": [], "registries": {}, "normalized": ""}, sys.stdout)
        return
    facts = Facts()
    facts.visit(tree)
    json.dump({"ok": True, "funcs": facts.funcs, "uses": facts.uses, "registries": facts.registries,
               "normalized": ast.dump(undocumented(tree), include_attributes=False)}, sys.stdout)


main()
