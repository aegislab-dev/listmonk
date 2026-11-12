import Papa from 'papaparse';
import {
  convertValue, normalizeName, standardByKey, standardFor,
} from './standard-attribs.js'; // eslint-disable-line import/extensions

export const LIMITS = Object.freeze({ input: 10 * 1024 * 1024, rows: 50000, output: 20 * 1024 * 1024 });
export const STORAGE_KEY = 'listmonk.axm.importMapping.v1';
const ROLES = ['email', 'name', 'attribute', 'tags', 'discard'];
const NEW_KEY = /^[a-z][a-z0-9_]*$/;

export class ImportError extends Error {
  constructor(code) {
    super(code);
    this.code = code;
  }
}

export function checkInputSize(size) {
  if (size > LIMITS.input) throw new ImportError('inputLimit');
}

export function inspectCSVHeader(text, delimiter = '') {
  if (delimiter && (delimiter.length !== 1 || /["\r\n\0]/.test(delimiter))) throw new ImportError('delimiter');
  const result = Papa.parse(text.replace(/^\uFEFF/, ''), {
    header: false, preview: 1, delimiter, delimitersToGuess: [',', ';', '\t', '|'],
  });
  const columns = (result.data[0] || []).map((cell) => cell.trim());
  return {
    delimiter: result.meta.delimiter,
    standard: !result.errors.some((error) => error.type === 'Quotes')
      && columns.includes('email') && columns.every((column) => ['email', 'name', 'attributes'].includes(column)),
  };
}

// Original uploads have no mapping size limit. Check UTF-8 in bounded chunks in
// the Worker, retaining only a header prefix, without copying the entire file.
export async function inspectOriginalCSV(file, delimiter = '') {
  const decoder = new TextDecoder('utf-8', { fatal: true });
  let prefix = '';
  const accept = (text) => {
    if (text.includes('\0')) throw new ImportError('nul');
    if (prefix.length < 65536) prefix += text.slice(0, 65536 - prefix.length);
  };
  try {
    for (let offset = 0; offset < file.size; offset += 512 * 1024) {
      // eslint-disable-next-line no-await-in-loop
      const bytes = await file.slice(offset, offset + 512 * 1024).arrayBuffer();
      accept(decoder.decode(bytes, { stream: true }));
    }
    accept(decoder.decode());
  } catch (error) {
    if (error instanceof ImportError) throw error;
    throw new ImportError('rawEncoding');
  }
  return { encoding: 'utf-8', ...inspectCSVHeader(prefix, delimiter) };
}

// CP949 must also be tested in Chromium: Node's euc-kr decoder differs.
export function decodeCSV(bytes, encoding = 'auto') {
  checkInputSize(bytes.byteLength);
  const input = new Uint8Array(bytes);
  const bom = input[0] === 0xef && input[1] === 0xbb && input[2] === 0xbf;
  let candidates = [encoding === 'cp949' ? 'euc-kr' : 'utf-8'];
  if (encoding === 'auto') candidates = bom ? ['utf-8'] : ['utf-8', 'euc-kr'];
  let result;
  candidates.some((candidate) => {
    try {
      result = { text: new TextDecoder(candidate, { fatal: true }).decode(input), encoding: candidate === 'euc-kr' ? 'cp949' : 'utf-8' };
      return true;
    } catch (_) {
      return false;
    }
  });
  if (!result) throw new ImportError('decode');
  if (result.text.includes('\0')) throw new ImportError('nul');
  return result;
}

export function cleanEmail(value) {
  return String(value).replace(/[\uFEFF\u200B-\u200D\u2060]/g, '').trim()
    .replace(/^<\s*(.*?)\s*>$/, '$1')
    .trim()
    .toLowerCase();
}

export function validEmail(value) {
  // CSV imports reject embedded ASCII/C1 controls as well as whitespace.
  // eslint-disable-next-line no-control-regex
  if (/[\s\u0000-\u001f\u007f-\u009f]/.test(value)) return false;
  const parts = value.split('@');
  if (parts.length !== 2 || !parts[0] || !parts[1]) return false;
  const domain = parts[1];
  return domain.includes('.') && !domain.startsWith('.') && !domain.endsWith('.');
}

export function parseCSV(text, { delimiter = '', header = true } = {}) {
  if (text.includes('\0')) throw new ImportError('nul');
  if (delimiter && (delimiter.length !== 1 || /["\r\n\0]/.test(delimiter))) throw new ImportError('delimiter');
  const result = Papa.parse(text.replace(/^\uFEFF/, ''), {
    header: false,
    delimiter,
    delimitersToGuess: [',', ';', '\t', '|'],
    skipEmptyLines: false,
  });
  // A quote error can consume all later physical lines as one logical record.
  // Their boundaries/count cannot be recovered safely; reject the whole file.
  if (result.errors.some((error) => error.type === 'Quotes')) throw new ImportError('quotes');
  const nonempty = result.data.map((cells, index) => ({ cells, row: index + 1 }))
    .filter(({ cells }) => !(cells.length === 1 && cells[0].trim() === ''));
  const first = nonempty[0];
  if (!first) throw new ImportError('empty');
  const columns = first.cells.map((cell, index) => (header ? cell.trim() : `Column ${index + 1}`));
  const data = header ? nonempty.slice(1) : nonempty;
  if (data.length > LIMITS.rows) throw Object.assign(new ImportError('rowLimit'), { total: data.length });
  const issues = data.filter(({ cells }) => cells.length !== columns.length)
    .map(({ row }) => ({ row, type: 'width' }));
  const excluded = new Set(issues.map(({ row }) => row));
  return {
    columns,
    rows: data.filter(({ row }) => !excluded.has(row)),
    issues,
    total: data.length,
    delimiter: result.meta.delimiter,
    headerHint: header && first.cells.some((cell) => validEmail(cleanEmail(cell))),
    standard: header && columns.includes('email') && columns.every((column) => ['email', 'name', 'attributes'].includes(column)),
  };
}

// Common Korean list headers. Spaces are ignored when a label is looked up. The standard
// attributes (country, follower, company, title, industry) are not here: their column names
// come from the server's dictionary and are matched first.
const FIELDS = { email: ['이메일', '메일', '메일주소', '이메일주소', 'e-mail', 'email'], name: ['이름', '성함', '성명', 'name'], discard: ['메모', '비고'] };
const KEYS = {
  department: ['부서', '부서명'],
  phone: ['전화', '전화번호', '연락처', '휴대폰', '휴대폰번호', '휴대전화', '핸드폰'],
  address: ['주소'],
  postal_code: ['우편번호'],
  city: ['도시'],
  signup_date: ['가입일', '가입일자', '등록일'],
  birth_date: ['생년월일', '생일'],
  gender: ['성별'],
  age: ['나이', '연령'],
  website: ['홈페이지', '웹사이트'],
  customer_grade: ['고객등급'],
  member_grade: ['회원등급'],
  grade: ['등급'],
  member_no: ['회원번호'],
  customer_no: ['고객번호'],
  tags: ['태그', '관심태그', '관심사'],
};
const KNOWN = new Map(Object.entries(KEYS).flatMap(([key, labels]) => labels.map((label) => [label, key])));
const compact = (label) => String(label).replace(/\s+/g, '').toLowerCase();

const INITIAL = ['g', 'kk', 'n', 'd', 'tt', 'r', 'm', 'b', 'pp', 's', 'ss', '', 'j', 'jj', 'ch', 'k', 't', 'p', 'h'];
const MEDIAL = ['a', 'ae', 'ya', 'yae', 'eo', 'e', 'yeo', 'ye', 'o', 'wa', 'wae', 'oe', 'yo', 'u', 'wo', 'we', 'wi', 'yu', 'eu', 'ui', 'i'];
const FINAL = ['', 'k', 'k', 'k', 'n', 'n', 'n', 't', 'l', 'k', 'm', 'l', 'l', 'l', 'p', 'l', 'm', 'p', 'p', 't', 't', 'ng', 't', 't', 'k', 't', 'p', 't'];

// Spells Hangul syllables in Latin letters (Revised Romanization, per syllable).
export function romanize(text) {
  return String(text).replace(/[가-힣]/g, (syllable) => {
    const code = syllable.charCodeAt(0) - 0xac00;
    return INITIAL[Math.floor(code / 588)] + MEDIAL[Math.floor(code / 28) % 21] + FINAL[code % 28];
  });
}

// Tidies any text into a valid new attribute key, or '' if nothing usable is left.
function spellKey(text) {
  const key = romanize(text).replace(/([a-z0-9])([A-Z])/g, '$1_$2').toLowerCase().replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
  return /^[0-9]/.test(key) ? `item_${key}` : key;
}

// A valid new attribute key for a label or a mistyped key: the standard item it means, then
// a known term, else spelled out. standard is the server's dictionary ([{ key, aliases }]).
export function suggestKey(text, standard = []) {
  return standardFor(standard, text)?.key || KNOWN.get(compact(text)) || spellKey(text);
}

// The standard item a key that is not standard itself stands for ('followers' -> follower).
export function standardTwin(key, standard = []) {
  const entry = standardFor(standard, key);
  return entry && entry.key !== key ? entry.key : '';
}

// True when the key was spelled out from Hangul rather than taken from KEYS.
export function isSpelledKey(label, key) {
  const spelled = spellKey(label);
  return /[가-힣]/.test(label) && !!spelled && (key === spelled || key.startsWith(`${spelled}_`));
}

// Suggests where each column goes. A column name the standard dictionary knows gets the
// standard key; any other column gets a key already in use that differs from its name only
// by letter case, spaces, underscores or hyphens, before a new key is made up.
export function suggestMapping(parsed, memory = { columns: {} }, { standard = [], existingKeys = [] } = {}) {
  const remembered = (column) => {
    const entry = Object.prototype.hasOwnProperty.call(memory.columns || {}, column) && memory.columns[column];
    if (!entry || !ROLES.includes(entry.role)) return null;
    // A name this screen made up earlier for a standard column (region, job_title, palrowo)
    // gives way to the standard key. A name the user chose on purpose is kept.
    const entryOf = standardFor(standard, column);
    const spelled = spellKey(column);
    const madeUp = standardFor(standard, entry.key) === entryOf || (spelled && (entry.key === spelled || entry.key.startsWith(`${spelled}_`)));
    if (entry.role === 'attribute' && entryOf && entry.key !== entryOf.key && madeUp) return null;
    return { role: entry.role, key: entry.key || '' };
  };
  // Names the user chose before are kept; suggested names must not collide with them or each other.
  const used = new Set(parsed.columns.map((column) => remembered(column)?.key).filter(Boolean));
  const existing = (column) => {
    const wanted = normalizeName(column);
    const same = existingKeys.filter((key) => normalizeName(key) === wanted && !used.has(key));
    return same.find((key) => key === column) || same[0] || '';
  };
  const unique = (column, index) => {
    const entry = standardFor(standard, column);
    let key = (entry && !used.has(entry.key) && entry.key) || existing(column)
      || KNOWN.get(compact(column)) || spellKey(column) || `column_${index + 1}`;
    if (used.has(key)) key = spellKey(column) || key;
    const base = key;
    for (let n = 2; used.has(key); n += 1) key = `${base}_${n}`;
    used.add(key);
    return key;
  };
  return parsed.columns.map((column, index) => {
    const known = remembered(column);
    if (known) return known;
    const label = compact(column);
    const field = Object.keys(FIELDS).find((role) => FIELDS[role].includes(label));
    if (field) return { role: field, key: '' };
    const samples = parsed.rows.slice(0, 20).map(({ cells }) => cells[index]).filter((v) => v.trim());
    if (samples.length && samples.filter((v) => validEmail(cleanEmail(v))).length > samples.length / 2) return { role: 'email', key: '' };
    return { role: KNOWN.get(label) === 'tags' ? 'tags' : 'attribute', key: unique(column, index) };
  });
}

export function validateMapping(mapping, existingKeys = []) {
  const errors = [];
  if (mapping.filter(({ role }) => role === 'email').length !== 1) errors.push({ type: 'emailColumns' });
  if (mapping.filter(({ role }) => role === 'name').length > 1) errors.push({ type: 'nameColumns' });
  const keys = new Set();
  mapping.forEach(({ role, key }, index) => {
    if (!ROLES.includes(role)) errors.push({ type: 'role', index });
    if (role !== 'attribute' && role !== 'tags') return;
    if (!key || (!existingKeys.includes(key) && !NEW_KEY.test(key))) errors.push({ type: 'key', index });
    if (keys.has(key)) errors.push({ type: 'duplicateKey', index });
    keys.add(key);
  });
  return errors;
}

// Builds the upload from the mapping. A column saved under a standard key has its values
// brought into that item's format (country name -> code, 10K -> 10000 as a JSON number).
// values reports, per such column, how many cells were kept, changed, left out as blank and
// could not be read, with examples. Unreadable cells are never stored: the caller uploads
// only when every column that has them was told to leave them out (mapping[i].blank).
export function convertCSV(parsed, mapping, existingKeys = [], standard = []) {
  if (mapping.length !== parsed.columns.length || validateMapping(mapping, existingKeys).length) throw new ImportError('mapping');
  const emailIndex = mapping.findIndex(({ role }) => role === 'email');
  const nameIndex = mapping.findIndex(({ role }) => role === 'name');
  const seen = new Set();
  const summary = {
    imported: 0, cleaned: 0, empty: 0, invalid: 0, duplicate: 0, malformed: parsed.issues.length,
  };
  const examples = {
    empty: [], invalid: [], duplicate: [], malformed: parsed.issues.slice(0, 5),
  };
  const values = [];
  const columnValues = mapping.map(({ role, key }, index) => {
    const kind = role === 'attribute' ? standardByKey(standard, key)?.kind : '';
    if (kind !== 'country' && kind !== 'integer') return null;
    const stats = {
      index, key, kind, kept: 0, changed: 0, blank: 0, unreadable: 0, conversions: [], examples: [],
    };
    values.push(stats);
    return stats;
  });
  const output = [];
  const sourceRows = [];
  const preview = [];
  parsed.rows.forEach(({ cells, row }) => {
    const email = cleanEmail(cells[emailIndex]);
    if (email !== cells[emailIndex]) summary.cleaned += 1;
    let issue;
    if (!email) issue = 'empty';
    else if (!validEmail(email)) issue = 'invalid';
    else if (seen.has(email)) issue = 'duplicate';
    if (issue) {
      summary[issue] += 1;
      if (examples[issue].length < 5) examples[issue].push({ row, email });
      return;
    }
    seen.add(email);
    const attributes = Object.create(null);
    // What the preview shows next to a value: the cell it was read from, or a cell left out.
    const changed = Object.create(null);
    const dropped = [];
    mapping.forEach(({ role, key }, index) => {
      const value = cells[index].trim();
      if (!value || (role !== 'attribute' && role !== 'tags')) return;
      const stats = columnValues[index];
      if (stats) {
        const result = convertValue(stats.kind, value);
        stats[result.status] += 1;
        if (result.status === 'changed') {
          changed[key] = value;
          if (stats.conversions.length < 5 && !stats.conversions.some(({ from }) => from === value)) stats.conversions.push({ from: value, to: String(result.value) });
        } else if (result.status === 'unreadable') {
          dropped.push([key, value]);
          if (stats.examples.length < 5 && !stats.examples.some((example) => example.value === value)) stats.examples.push({ row, value });
        }
        if (result.status === 'kept' || result.status === 'changed') attributes[key] = result.value;
        return;
      }
      attributes[key] = role === 'tags' ? [...new Set(value.split(',').map((tag) => tag.trim()).filter(Boolean))] : value;
      if (role === 'tags' && !attributes[key].length) delete attributes[key];
    });
    const name = nameIndex < 0 ? '' : cells[nameIndex].trim();
    const serialized = Object.keys(attributes).length ? JSON.stringify(attributes) : '';
    output.push([email, name, serialized]);
    sourceRows.push(row);
    // The first rows, and after them the first rows that have a cell left out.
    if (preview.length < 5 || (dropped.length && preview.filter((shown) => shown.dropped.length).length < 3 && preview.length < 8)) {
      preview.push({
        row, email, name, attributes: Object.entries(attributes), changed: { ...changed }, dropped,
      });
    }
  });
  const csv = Papa.unparse({ fields: ['email', 'name', 'attributes'], data: output }, { delimiter: ',', newline: '\r\n' });
  const bytes = new TextEncoder().encode(csv);
  if (bytes.byteLength > LIMITS.output) throw new ImportError('outputLimit');
  summary.imported = output.length;
  return {
    bytes, summary, examples, preview, sourceRows, values,
  };
}

// The columns whose unreadable cells the user has not yet agreed to leave out.
export function unresolvedValues(values, mapping) {
  return values.filter(({ index, unreadable }) => unreadable > 0 && !mapping[index].blank).map(({ index }) => index);
}

function safeMemory(value) {
  const columns = Object.create(null);
  Object.entries(value?.columns || {}).slice(-200).forEach(([column, entry]) => {
    if (entry && ROLES.includes(entry.role) && typeof entry.key === 'string') columns[column] = { role: entry.role, key: entry.key };
  });
  const keys = [...new Set((Array.isArray(value?.keys) ? value.keys : []).filter((key) => typeof key === 'string' && key))].slice(-200);
  return { columns, keys };
}

export function readMemory(storage) {
  try {
    return safeMemory(JSON.parse(storage.getItem(STORAGE_KEY)));
  } catch (_) {
    return safeMemory(null);
  }
}

export function rememberMapping(storage, columns, mapping, old = readMemory(storage)) {
  const next = safeMemory(old);
  columns.forEach((column, index) => {
    // Only column labels, roles and attribute keys are persisted, never cells.
    if (column && mapping[index]) next.columns[column] = { role: mapping[index].role, key: mapping[index].key || '' };
  });
  next.keys = [...next.keys, ...mapping.filter(({ role }) => role === 'attribute' || role === 'tags').map(({ key }) => key)];
  const memory = safeMemory(next);
  try {
    storage.setItem(STORAGE_KEY, JSON.stringify(memory));
  } catch (_) {
    // Storage is optional (private browsing, full quota, or disabled).
  }
  return memory;
}
