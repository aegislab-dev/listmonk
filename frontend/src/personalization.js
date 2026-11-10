// Keep the persisted format compatible with listmonk's Go templates.
export const BASE_ITEMS = [
  { id: 'name', expression: '.Subscriber.Name' },
  { id: 'email', expression: '.Subscriber.Email' },
];
// Go identifiers accept decimal digits, not every Unicode number (for example ½).
const identifier = /^[\p{L}_][\p{L}\p{Nd}_]*$/u;

// Punctuation must survive both Markdown and HTML rendering, including }}.
export function goString(value) {
  let out = '"';
  Array.from(String(value)).forEach((char) => {
    const cp = char.codePointAt(0);
    if (cp >= 0xd800 && cp <= 0xdfff) throw new Error('Invalid Unicode');
    if (char === ' ' || /^[\p{L}\p{N}]$/u.test(char)) {
      out += char;
    } else if (cp <= 0xffff) {
      out += `\\u${cp.toString(16).padStart(4, '0')}`;
    } else {
      out += `\\U${cp.toString(16).padStart(8, '0')}`;
    }
  });
  return `${out}"`;
}

export function buildToken(item, fallback = '') {
  const base = BASE_ITEMS.find((v) => v.id === item.id);
  let ref = base?.expression;
  if (!ref) {
    ref = identifier.test(item.key) ? `.Subscriber.Attribs.${item.key}`
      : `index .Subscriber.Attribs ${goString(item.key)}`;
  }
  return `{{ ${ref}${fallback ? ` | default ${goString(fallback)}` : ''} }}`;
}

function readString(value) {
  return value.slice(1, -1).replace(/\\(u[\da-fA-F]{4}|U[\da-fA-F]{8})/g, (_, code) => {
    const cp = parseInt(code.slice(1), 16);
    if (cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff)) throw new Error('Invalid Unicode');
    return String.fromCodePoint(cp);
  });
}

// Deliberately recognize only our references and optional default pipeline.
// Everything else (TrackLink, if, custom functions) stays byte-for-byte text.
const quoted = '"(?:[^"\\\\\\r\\n]|\\\\u[\\da-fA-F]{4}|\\\\U[\\da-fA-F]{8})*"';
const reference = '(?:\\.Subscriber\\.(Name|Email)|\\.Subscriber\\.Attribs\\.([\\p{L}_][\\p{L}\\p{Nd}_]*)';
const tokenPattern = new RegExp(`^\\{\\{\\s*${reference}|index\\s+\\.Subscriber\\.Attribs\\s+(${quoted}))`
  + `\\s*(?:\\|\\s*default\\s+(${quoted})\\s*)?\\}\\}$`, 'u');

export function parseTokens(text) {
  const parts = [];
  let start = 0;
  Array.from(text.matchAll(/\{\{[\s\S]*?\}\}/g)).forEach((match) => {
    const parsed = tokenPattern.exec(match[0]);
    if (parsed) {
      try {
        const key = parsed[2] ?? (parsed[3] ? readString(parsed[3]) : undefined);
        const item = parsed[1] ? BASE_ITEMS.find((v) => v.id === parsed[1].toLowerCase())
          : { id: `attrib:${key}`, key };
        const fallback = parsed[4] ? readString(parsed[4]) : '';
        if (match.index > start) parts.push({ type: 'text', value: text.slice(start, match.index) });
        parts.push({
          type: 'token', raw: match[0], item, fallback,
        });
        start = match.index + match[0].length;
      } catch { /* Preserve unsupported strings as text. */ }
    }
  });
  if (start < text.length) parts.push({ type: 'text', value: text.slice(start) });
  return parts;
}

export function collectAttribKeys(subscribers) {
  const keys = new Set();
  subscribers.forEach((subscriber) => {
    Object.entries(subscriber.attribs || {}).forEach(([key, value]) => {
      if (['string', 'number', 'boolean'].includes(typeof value)) keys.add(key);
    });
  });
  return [...keys].sort();
}

export async function loadItems(api, listIds, labels = {}) {
  const ids = [...new Set(listIds)];
  const status = { noLists: !ids.length, truncated: ids.length > 5, error: false };
  try {
    const samples = await Promise.all(ids.slice(0, 5).map((id) => api.getSubscriberAttribSample({
      list_id: id, page: 1, per_page: 50, order_by: 'id', order: 'desc',
    })));
    const keys = collectAttribKeys(samples.flatMap((s) => (s.results || []).slice(0, 50)));
    return {
      ...status,
      items: [...BASE_ITEMS, ...keys.map((key) => ({
        id: `attrib:${key}`, key, label: Object.hasOwn(labels, key) ? labels[key] : key,
      }))],
    };
  } catch {
    return { ...status, items: [...BASE_ITEMS], error: true };
  }
}
