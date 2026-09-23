// Two-way YAML ⇄ JSON sync for Dockge-style compose editing.
// Ports of dockge-mod common/util-common.ts: copyYAMLComments + envsubstYAML,
// built on the `yaml` package's AST (Document) so comments survive round-trips.

import { Document, Pair, Scalar, parseDocument } from 'yaml';

export interface YamlParseResult {
  config: Record<string, any>;
  doc: Document;
}

/**
 * Parse compose YAML into a plain JS object + keep the AST for comment
 * preservation. Throws the first YAML error. Enforces that `services`,
 * when present, is a plain mapping (Dockge parity).
 */
export function yamlToJson(content: string): YamlParseResult {
  const doc = parseDocument(content);
  if (doc.errors.length > 0) {
    throw doc.errors[0];
  }
  const config = (doc.toJS() ?? {}) as Record<string, any>;

  if (!config.services) {
    config.services = {};
  }
  if (Array.isArray(config.services) || typeof config.services !== 'object') {
    throw new Error('Services must be an object');
  }
  return { config, doc };
}

/**
 * Rebuild YAML text from the plain JSON config, grafting comments back from
 * the previous AST (matched by content, not index — Dockge's approach).
 */
export function jsonToYaml(config: Record<string, any>, prevDoc: Document | null): { yaml: string; doc: Document } {
  const doc = new Document(config);
  if (prevDoc) {
    copyYAMLComments(doc, prevDoc);
  }
  return { yaml: doc.toString(), doc };
}

/** Copy yaml comments from src Document into doc. */
export function copyYAMLComments(doc: Document, src: Document): void {
  (doc as any).comment = (src as any).comment;
  (doc as any).commentBefore = (src as any).commentBefore;

  if (doc?.contents && src?.contents) {
    copyYAMLCommentsItems((doc.contents as any).items, (src.contents as any).items);
  }
}

/**
 * Copy comments between item lists, matching source items by content
 * rather than index (Dockge's approach).
 *
 * yaml v2.9 note: an end-of-line comment (`web: # note`) is stored on the
 * VALUE node's commentBefore and JSON.stringify includes comment fields —
 * so a commented source node never stringifies equal to a fresh target
 * node. We therefore match Pairs by key only (matching Dockge behavior for
 * mapping keys like service names) and strip comment fields when comparing.
 */
function stripComments(obj: any): any {
  if (obj === null || typeof obj !== 'object') return obj;
  if (Array.isArray(obj)) return obj.map(stripComments);
  const out: Record<string, any> = {};
  for (const [k, v] of Object.entries(obj)) {
    if (k === 'comment' || k === 'commentBefore') continue;
    out[k] = stripComments(v);
  }
  return out;
}

function copyYAMLCommentsItems(items: any[] | undefined, srcItems: any[] | undefined): void {
  if (!items || !srcItems) return;

  for (let i = 0; i < items.length; i++) {
    const item = items[i];
    const isPair = item?.key !== undefined;

    const srcIndex = srcItems.findIndex((srcItem: any) => {
      if (isPair) {
        // Mapping pair: match by key (Dockge parity for service names etc.)
        return JSON.stringify(srcItem.key) === JSON.stringify(item.key);
      }
      // Scalar sequence item: match by value, ignoring comments
      return JSON.stringify(stripComments(srcItem.value ?? srcItem)) ===
        JSON.stringify(stripComments(item.value ?? item));
    });

    if (srcIndex !== -1) {
      const srcItem = srcItems[srcIndex];
      const nextSrcItem = srcItems[srcIndex + 1];

      if (item.key && srcItem.key) {
        item.key.comment = srcItem.key.comment;
        item.key.commentBefore = srcItem.key.commentBefore;
      }

      if (srcItem.comment) {
        item.comment = srcItem.comment;
      }

      // Comments between array items
      if (nextSrcItem && nextSrcItem.commentBefore && items[i + 1]) {
        items[i + 1].commentBefore = nextSrcItem.commentBefore;
      }

      // Trailing comments after array items
      if (srcItem.value && srcItem.value.comment && item.value) {
        item.value.comment = srcItem.value.comment;
      }

      if (item.value && srcItem.value) {
        if (typeof item.value === 'object' && typeof srcItem.value === 'object') {
          item.value.comment = srcItem.value.comment;
          item.value.commentBefore = srcItem.value.commentBefore;

          if (item.value.items && srcItem.value.items) {
            copyYAMLCommentsItems(item.value.items, srcItem.value.items);
          }
        }
      }
    }
  }
}

/**
 * Substitute ${VAR} and $VAR references from an env map. Mirrors the
 * @inventage/envsubst behavior Dockge uses: simple $VAR / ${VAR} expansion,
 * leaving unknown variables empty.
 */
export function envsubst(input: string, variables: Record<string, string>): string {
  return input.replace(/\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)/g, (_m, braced, plain) => {
    const name = braced ?? plain;
    return Object.prototype.hasOwnProperty.call(variables, name) ? variables[name] : '';
  });
}

/**
 * Structure-preserving ${VAR} expansion of a compose YAML string, emulating
 * how docker compose applies environment variables (Dockge envsubstYAML port).
 */
export function envsubstYAML(content: string, env: Record<string, string>): string {
  const doc = parseDocument(content);
  if (doc.contents) {
    for (const item of (doc.contents as any).items) {
      traverseYAML(item, env);
    }
  }
  return doc.toString();
}

function traverseYAML(pair: any, env: Record<string, string>): void {
  if (pair.value && pair.value.items) {
    for (const item of pair.value.items) {
      if (item instanceof Pair) {
        traverseYAML(item, env);
      } else if (item instanceof Scalar) {
        const value: unknown = item.value;
        if (typeof value === 'string') {
          item.value = envsubst(value, env);
        }
      }
    }
  } else if (pair.value && typeof pair.value.value === 'string') {
    pair.value.value = envsubst(pair.value.value, env);
  }
}

