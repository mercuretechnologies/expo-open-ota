import { parse } from '@babel/parser';
import * as t from '@babel/types';

export function mergeEnvironment(
  remote: Record<string, string>,
  file: Record<string, string>,
  channel?: string
): Record<string, string> {
  const merged = { ...remote, ...file };
  if (channel && !Object.prototype.hasOwnProperty.call(merged, 'RELEASE_CHANNEL')) {
    merged.RELEASE_CHANNEL = channel;
  }
  return merged;
}

// Invoked on Metro's input source, before Expo's EXPO_PUBLIC_* inlining.
// NODE_ENV is set by this build. Dependency internals are excluded by the
// transformer wrapper; dynamic/aliased env objects fail closed unless bypassed.
export function checkEnvironment(source: string, filename: string, keys: string[]): void {
  const references = collectEnvReferences(parseSource(source, filename));
  const known = new Set([...keys, 'NODE_ENV', 'CI', 'EXPO_NO_DOTENV']);
  const missing = [...references.names].filter(name => !known.has(name)).sort();
  if (!missing.length && !references.dynamic) {
    return;
  }
  const problems = [
    missing.length ? `missing keys ${missing.join(', ')}` : '',
    references.dynamic ? 'dynamic or aliased process.env access cannot be verified' : '',
  ].filter(Boolean);
  throw new Error(
    `Environment check failed in ${filename}: ${problems.join(
      '; '
    )}. Supply server/--env-file variables or explicitly use --ignore-env-check.`
  );
}

function parseSource(source: string, filename: string): t.File {
  try {
    return parse(source, {
      sourceType: 'unambiguous',
      plugins: [/\.tsx?$/.test(filename) ? 'typescript' : 'flow', 'jsx'],
      errorRecovery: false,
    });
  } catch {
    throw new Error(
      `Environment check could not parse ${filename}; no source excerpt is shown. Fix syntax or explicitly use --ignore-env-check.`
    );
  }
}

interface EnvReferences {
  // Statically named keys read from process.env.
  names: Set<string>;
  // process or process.env escaped into a form this check cannot follow.
  dynamic: boolean;
}

function collectEnvReferences(ast: t.File): EnvReferences {
  const found: EnvReferences = { names: new Set(), dynamic: false };
  const record = (name: string | undefined): void => {
    if (name === undefined) {
      found.dynamic = true;
    } else {
      found.names.add(name);
    }
  };
  walk(ast, (node, parent) => {
    if (t.isIdentifier(node, { name: 'process' }) && isUsedAsValue(node, parent)) {
      found.dynamic = true;
    }
    if (!isProcessEnv(node)) {
      return;
    }
    if (isMember(parent) && parent.object === node) {
      // process.env.KEY, process.env['KEY'], process.env[expression]
      record(propertyName(parent));
    } else if (
      t.isVariableDeclarator(parent) &&
      parent.init === node &&
      t.isObjectPattern(parent.id)
    ) {
      // const { KEY, ...rest } = process.env
      for (const entry of parent.id.properties) {
        record(t.isObjectProperty(entry) ? keyName(entry) : undefined);
      }
    } else {
      found.dynamic = true;
    }
  });
  return found;
}

function walk(node: t.Node, visit: (node: t.Node, parent?: t.Node) => void, parent?: t.Node): void {
  visit(node, parent);
  for (const key of t.VISITOR_KEYS[node.type] ?? []) {
    const child = (node as unknown as Record<string, unknown>)[key];
    for (const value of Array.isArray(child) ? child : [child]) {
      if (value && typeof value === 'object' && 'type' in value) {
        walk(value as t.Node, visit, node);
      }
    }
  }
}

type Member = t.MemberExpression | t.OptionalMemberExpression;

function isMember(node: t.Node | null | undefined): node is Member {
  return !!node && (t.isMemberExpression(node) || t.isOptionalMemberExpression(node));
}

function isProcessEnv(node: t.Node): node is Member {
  return (
    isMember(node) &&
    t.isIdentifier(node.object, { name: 'process' }) &&
    propertyName(node) === 'env'
  );
}

// `const p = process`, `x = process` or `f(process)` could reach env indirectly.
function isUsedAsValue(node: t.Identifier, parent?: t.Node): boolean {
  return (
    (t.isVariableDeclarator(parent) && parent.init === node) ||
    (t.isAssignmentExpression(parent) && parent.right === node) ||
    (t.isCallExpression(parent) && parent.arguments.includes(node))
  );
}

function propertyName(node: Member): string | undefined {
  if (!node.computed && t.isIdentifier(node.property)) {
    return node.property.name;
  }
  if (t.isStringLiteral(node.property)) {
    return node.property.value;
  }
  return undefined;
}

function keyName(entry: t.ObjectProperty): string | undefined {
  if (!entry.computed && t.isIdentifier(entry.key)) {
    return entry.key.name;
  }
  if (t.isStringLiteral(entry.key)) {
    return entry.key.value;
  }
  return undefined;
}
