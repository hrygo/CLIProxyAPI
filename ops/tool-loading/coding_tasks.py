"""Small coding tasks with host-owned behavioral gold and restricted execution.

This is a pure-function pilot corpus, not a general Python sandbox or a claim
that arbitrary repositories are covered. The client can replace one source
file; the scoring implementation and cases never live in its workspace.
"""

from __future__ import annotations

import ast
import asyncio
from copy import deepcopy
from dataclasses import dataclass
import hashlib
import json
import os
import operator
from pathlib import Path
import stat
import subprocess
import sys

MAX_SOURCE_BYTES = 8192
WORKER_SECONDS = 2


@dataclass(frozen=True)
class CodingTask:
    function: str
    arity: int
    requirements: str
    baseline: str
    cases: tuple


TASKS = {
    "budget": CodingTask("remaining_budget", 2,
        "Implement remaining_budget(limit, entries). limit must be an int (not bool), entries a list. "
        "Each entry is a dict with integer amount (not bool) and state. Subtract only posted amounts "
        "from limit; negative posted amounts are refunds. Pending/void entries do not affect the total. "
        "Invalid argument types raise ValueError. Entry keys and states are otherwise well formed.",
        "def remaining_budget(limit, entries):\n    total = 0\n    for entry in entries:\n        total += entry['amount']\n    return limit - total\n",
        (([100, []], 100), ([100, [{"amount": 20, "state": "posted"}]], 80),
         ([100, [{"amount": 20, "state": "pending"}]], 100),
         ([100, [{"amount": 30, "state": "void"}, {"amount": -10, "state": "posted"}]], 110),
         ([5, [{"amount": 20, "state": "posted"}]], -15),
         ([0, [{"amount": 0, "state": "posted"}]], 0),
         ([True, []], "ValueError"), (["100", []], "ValueError"),
         ([100, {}], "ValueError"), ([100, [{"amount": True, "state": "pending"}]], "ValueError"),
         ([100, [2]], "ValueError"))),
    "retry": CodingTask("retryable", 2,
        "Implement retryable(status, exposed). status must be int (not bool), exposed must be bool; "
        "otherwise raise ValueError. Once any output is exposed, never retry. Before exposure retry "
        "429 and 500..599 inclusive, except 501 and 505. All other status codes are not retryable.",
        "def retryable(status, exposed):\n    return status >= 500\n",
        tuple(([status, exposed], not exposed and (status == 429 or 500 <= status <= 599 and status not in (501, 505)))
              for status, exposed in ((200, False), (429, False), (499, False), (500, False), (501, False),
                                      (502, False), (505, False), (599, False), (600, False),
                                      (429, True), (500, True), (599, True)))
        + (([True, False], "ValueError"), (["500", False], "ValueError"), ([500, 0], "ValueError"))),
    "identity": CodingTask("unique_call_ids", 1,
        "Implement unique_call_ids(values). values must be a list of strings; invalid types raise "
        "ValueError. Ignore empty strings, deduplicate exact IDs, and preserve the first occurrence "
        "order. Do not sort, trim, or change case. Return a list.",
        "def unique_call_ids(values):\n    return sorted(set(values))\n",
        (([[]], []), ([["b", "a", "b"]], ["b", "a"]), ([["", "a", "", "b"]], ["a", "b"]),
         ([["A", "a", "A"]], ["A", "a"]), ([[" a", "a", " a"]], [" a", "a"]),
         ([["x"]], ["x"]), ([["1", "10", "1", "2"]], ["1", "10", "2"]),
         (["abc"], "ValueError"), ([["a", 1]], "ValueError"), ([[False]], "ValueError"))),
}

LANGUAGE_CONTRACT = (
    "Use one plain Python function with the required name and argument count. "
    "Imports, attributes/methods, decorators, annotations, helper functions and dynamic calls are "
    "not allowed in this bounded pilot. Lists/dicts, if/for/while, operators and the builtins "
    "type, len, range, sum, min, max, sorted, set, list, dict, int, str, bool, abs, "
    "ValueError and TypeError are supported. Comprehensions, exponentiation and conversions via "
    "str/int/bool calls are excluded. Use result += [value] instead of list.append."
)

BUILTINS = {name: getattr(__import__("builtins"), name) for name in (
    "type", "len", "range", "sum", "min", "max", "sorted", "set", "list", "dict", "int", "str", "bool", "abs", "ValueError", "TypeError")}
ALLOWED_NODES = {getattr(ast, name) for name in (
    "Module", "FunctionDef", "arguments", "arg", "Return", "Assign", "AugAssign", "If", "For", "While", "Break", "Continue", "Pass", "Raise", "Expr",
    "Name", "Load", "Store", "Constant", "List", "Tuple", "Set", "Dict", "Subscript", "Slice", "Call", "keyword",
    "IfExp", "Compare", "BoolOp", "BinOp", "UnaryOp",
    "Add", "Sub", "Mult", "Div", "FloorDiv", "Mod", "USub", "UAdd", "Not", "And", "Or",
    "Eq", "NotEq", "Lt", "LtE", "Gt", "GtE", "Is", "IsNot", "In", "NotIn")}


def validate_source(name, source):
    task = TASKS[name]
    if not isinstance(source, str) or not source or len(source.encode()) > MAX_SOURCE_BYTES:
        raise ValueError("unsupported source size")
    tree = ast.parse(source)
    if len(tree.body) != 1 or not isinstance(tree.body[0], ast.FunctionDef):
        raise ValueError("exactly one function is required")
    function = tree.body[0]
    args = function.args
    if (function.name != task.function or len(args.args) != task.arity or args.posonlyargs or args.kwonlyargs
            or args.vararg or args.kwarg or args.defaults or args.kw_defaults or function.decorator_list
            or function.returns or getattr(function, "type_params", []) or any(arg.annotation for arg in args.args)):
        raise ValueError("unsupported function declaration")
    nodes = list(ast.walk(tree))
    if len(nodes) > 2048 or sum(isinstance(node, ast.FunctionDef) for node in nodes) != 1:
        raise ValueError("unsupported source complexity")
    for node in nodes:
        if type(node) not in ALLOWED_NODES:
            raise ValueError("unsupported source construct")
        if isinstance(node, ast.Name) and node.id.startswith("_"):
            raise ValueError("private names are not available")
        if isinstance(node, ast.Call) and (not isinstance(node.func, ast.Name) or node.func.id not in BUILTINS or node.func.id in {"str", "int", "bool"}):
            raise ValueError("only declared builtins may be called")
        if isinstance(node, ast.Constant) and type(node.value) not in (str, int, float, bool, type(None)):
            raise ValueError("unsupported constant")
        if isinstance(node, ast.Constant) and type(node.value) is int and node.value.bit_length() > 64:
            raise ValueError("unsupported integer size")
    return compile(ast.fix_missing_locations(_BoundedCode().visit(tree)), "module.py", "exec")


class ExecutionBudget(Exception):
    pass


class _BoundedCode(ast.NodeTransformer):
    def generic_visit(self, node):
        node = super().generic_visit(node)
        for field in ("body", "orelse"):
            statements = getattr(node, field, None)
            if statements is not None and not isinstance(node, ast.Module):
                observed = []
                for statement in statements:
                    observed += [ast.Expr(ast.Call(ast.Name("__step", ast.Load()), [], [])), statement]
                setattr(node, field, observed)
        return node

    def visit_BinOp(self, node):
        return ast.Call(ast.Name("__binary", ast.Load()),
                        [ast.Constant(type(node.op).__name__), self.visit(node.left), self.visit(node.right)], [])

    def visit_AugAssign(self, node):
        if not isinstance(node.target, ast.Name):
            raise ValueError("augmented assignment requires a local variable")
        return ast.Assign([node.target], ast.Call(ast.Name("__binary", ast.Load()),
            [ast.Constant(type(node.op).__name__), ast.Name(node.target.id, ast.Load()), self.visit(node.value)], []))


def _bounded(value):
    if isinstance(value, (str, list, tuple, dict, set, range)) and len(value) > 1000:
        raise ExecutionBudget()
    if type(value) is int and value.bit_length() > 128:
        raise ExecutionBudget()
    return value


def _binary(name, left, right):
    if name == "Mult":
        for sequence, count in ((left, right), (right, left)):
            if isinstance(sequence, (str, list, tuple)) and type(count) is int and len(sequence) * max(0, count) > 1000:
                raise ExecutionBudget()
    if name == "Mod" and isinstance(left, str):
        raise ExecutionBudget()
    operation = {"Add": operator.add, "Sub": operator.sub, "Mult": operator.mul,
                 "Div": operator.truediv, "FloorDiv": operator.floordiv, "Mod": operator.mod}[name]
    return _bounded(operation(left, right))


def _namespace():
    steps = 0
    def step():
        nonlocal steps
        steps += 1
        if steps > 2000: raise ExecutionBudget()
    def wrap(function):
        def bounded(*args, **kwargs):
            return _bounded(function(*args, **kwargs))
        return bounded
    builtins = {name: wrap(function) if name in {"range", "sum", "min", "max", "sorted", "abs"} else function
                for name, function in BUILTINS.items()}
    return {"__builtins__": builtins, "__step": step, "__binary": _binary}


def _same(actual, expected):
    if type(actual) is not type(expected):
        return False
    if isinstance(actual, list):
        return len(actual) == len(expected) and all(_same(a, b) for a, b in zip(actual, expected))
    return actual == expected


def _failure(reason, tests=0):
    return {"passed": False, "tests": tests, "passed_tests": 0, "reason": reason}


def _worker(name, source):
    import resource
    resource.setrlimit(resource.RLIMIT_CPU, (1, 1))
    # Darwin rejects RLIMIT_DATA/RLIMIT_AS lowering on this runtime. The AST
    # instruction/container bounds apply on both platforms; Linux also caps AS.
    if sys.platform != "darwin":
        _, inherited_hard = resource.getrlimit(resource.RLIMIT_AS)
        memory_limit = 256 * 1024 * 1024 if inherited_hard == resource.RLIM_INFINITY else min(256 * 1024 * 1024, inherited_hard)
        resource.setrlimit(resource.RLIMIT_AS, (memory_limit, memory_limit))
    resource.setrlimit(resource.RLIMIT_FSIZE, (0, 0))
    task = TASKS[name]
    try:
        compiled = validate_source(name, source)
    except (ValueError, SyntaxError, RecursionError):
        return _failure("unsupported_source", len(task.cases))
    passed = 0
    for args, expected in task.cases:
        namespace = _namespace()
        try:
            exec(compiled, namespace)
            value = namespace[task.function](*deepcopy(args))
            correct = expected != "ValueError" and _same(value, expected)
        except ExecutionBudget:
            return _failure("execution_budget", len(task.cases))
        except ValueError:
            correct = expected == "ValueError"
        except Exception:
            correct = False
        passed += bool(correct)
    return {"passed": passed == len(task.cases), "tests": len(task.cases), "passed_tests": passed,
            "reason": "passed" if passed == len(task.cases) else "behavior_mismatch"}


def _payload(name, source):
    if name not in TASKS:
        raise ValueError("unknown coding task")
    if not isinstance(source, str) or len(source.encode()) > MAX_SOURCE_BYTES:
        raise ValueError("unsupported source size")
    return json.dumps({"task": name, "source": source}).encode()


def _result(code, output):
    if code != 0 or len(output) > 4096:
        return _failure("worker_failed")
    try:
        result = json.loads(output)
        if (set(result) != {"passed", "tests", "passed_tests", "reason"} or type(result["passed"]) is not bool
                or type(result["tests"]) is not int or type(result["passed_tests"]) is not int):
            raise ValueError("invalid worker result")
        return result
    except (ValueError, TypeError):
        return _failure("worker_failed")


def _command():
    return [sys.executable, "-I", "-S", str(Path(__file__).resolve()), "--worker"]


def evaluate_source(name, source):
    payload = _payload(name, source)
    try:
        result = subprocess.run(_command(), input=payload, capture_output=True, timeout=WORKER_SECONDS,
                                env={}, cwd=os.devnull.rsplit("/", 1)[0])
    except subprocess.TimeoutExpired:
        return _failure("execution_budget")
    return _result(result.returncode, result.stdout)


async def async_evaluate_source(name, source):
    payload = _payload(name, source)
    acquisition = asyncio.create_task(asyncio.create_subprocess_exec(*_command(), stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL, env={}, cwd=os.devnull.rsplit("/", 1)[0]))
    try:
        process = await asyncio.shield(acquisition)
    except asyncio.CancelledError:
        process = await acquisition
        if process.returncode is None:
            try: process.kill()
            except ProcessLookupError: pass
        await process.wait()
        raise
    try:
        output, _ = await asyncio.wait_for(process.communicate(payload), WORKER_SECONDS)
        return _result(process.returncode, output)
    except asyncio.TimeoutError:
        return _failure("execution_budget")
    finally:
        if process.returncode is None:
            try: process.kill()
            except ProcessLookupError: pass
        await process.wait()


class CodingProject:
    def __init__(self, root, task):
        if task not in TASKS:
            raise ValueError("unknown coding task")
        self.root = Path(root).absolute()
        if self.root.is_symlink() or not self.root.is_dir():
            raise ValueError("a real private workspace is required")
        metadata = self.root.stat()
        self.identity = (metadata.st_dev, metadata.st_ino)
        self.task = task

    def _directory(self):
        descriptor = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        metadata = os.fstat(descriptor)
        if (metadata.st_dev, metadata.st_ino) != self.identity:
            os.close(descriptor)
            raise ValueError("workspace identity changed")
        return descriptor

    @classmethod
    def create(cls, root, task):
        project = cls(root, task)
        directory = project._directory()
        try:
            descriptor = os.open("module.py", os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600, dir_fd=directory)
            with os.fdopen(descriptor, "wb") as stream:
                stream.write(TASKS[task].baseline.encode())
                stream.flush()
                os.fsync(stream.fileno())
        except OSError as error:
            raise ValueError("coding project must be fresh") from error
        finally:
            os.close(directory)
        return project

    def read(self):
        directory = self._directory()
        try:
            descriptor = os.open("module.py", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
            with os.fdopen(descriptor, "rb") as stream:
                metadata = os.fstat(stream.fileno())
                if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1 or metadata.st_size > MAX_SOURCE_BYTES:
                    raise ValueError("unsafe source file")
                content = stream.read(MAX_SOURCE_BYTES + 1)
                if len(content) > MAX_SOURCE_BYTES: raise ValueError("oversized source")
        except OSError as error:
            raise ValueError("source could not be read safely") from error
        finally:
            os.close(directory)
        return {"source": content.decode(), "source_sha256": hashlib.sha256(content).hexdigest()}

    def replace(self, expected_sha256, source):
        _payload(self.task, source)
        current = self.read()
        if expected_sha256 != current["source_sha256"]:
            raise ValueError("source revision mismatch")
        directory = self._directory()
        try:
            descriptor = os.open("module.py", os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
            with os.fdopen(descriptor, "r+b") as stream:
                metadata = os.fstat(stream.fileno())
                if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1:
                    raise ValueError("unsafe source file")
                actual = stream.read(MAX_SOURCE_BYTES + 1)
                if len(actual) > MAX_SOURCE_BYTES or hashlib.sha256(actual).hexdigest() != expected_sha256:
                    raise ValueError("source revision changed before write")
                os.ftruncate(stream.fileno(), 0)
                stream.seek(0)
                stream.write(source.encode())
                stream.flush()
                os.fsync(stream.fileno())
        except OSError as error:
            raise ValueError("source could not be written safely") from error
        finally:
            os.close(directory)
        return self.read()

    def score(self):
        snapshot = self.read()
        return {**evaluate_source(self.task, snapshot["source"]), "source_sha256": snapshot["source_sha256"],
                "changed": snapshot["source"] != TASKS[self.task].baseline}


if __name__ == "__main__":
    if sys.argv[1:] != ["--worker"]:
        raise SystemExit(2)
    document = json.loads(sys.stdin.buffer.read(65536))
    print(json.dumps(_worker(document["task"], document["source"])))
