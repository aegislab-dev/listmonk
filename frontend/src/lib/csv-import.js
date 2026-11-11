import Papa from 'papaparse';

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

export function columnLetter(index) {
  let value = index + 1;
  let label = '';
  while (value > 0) {
    value -= 1;
    label = String.fromCharCode(65 + (value % 26)) + label;
    value = Math.floor(value / 26);
  }
  return label;
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

export function maskEmail(value) {
  const parts = cleanEmail(value).split('@');
  if (parts.length !== 2) return '***';
  const dot = parts[1].lastIndexOf('.');
  return `${parts[0].slice(0, 2)}***@${parts[1].slice(0, Math.min(2, dot < 0 ? 2 : dot))}***${dot < 0 ? '' : parts[1].slice(dot)}`;
}

// Also redact addresses inside names/attributes and columns assigned to discard.
export function maskSample(value, isEmail = false) {
  if (isEmail) return maskEmail(value);
  return String(value).replace(/[^\s<>"',;]+/g, (token) => (token.includes('@') ? maskEmail(token) : token));
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

const DICTIONARY = {
  이메일: ['email'],
  메일: ['email'],
  '메일 주소': ['email'],
  'e-mail': ['email'],
  email: ['email'],
  이름: ['name'],
  성함: ['name'],
  성명: ['name'],
  name: ['name'],
  회사: ['attribute', 'company_name'],
  회사명: ['attribute', 'company_name'],
  부서: ['attribute', 'department'],
  직책: ['attribute', 'job_title'],
  직함: ['attribute', 'job_title'],
  전화: ['attribute', 'phone'],
  전화번호: ['attribute', 'phone'],
  연락처: ['attribute', 'phone'],
  휴대폰: ['attribute', 'phone'],
  지역: ['attribute', 'region'],
  태그: ['tags', 'tags'],
  '관심 태그': ['tags', 'tags'],
  관심사: ['tags', 'tags'],
  메모: ['discard'],
  비고: ['discard'],
};

export function suggestMapping(parsed, memory = { columns: {} }) {
  return parsed.columns.map((column, index) => {
    const remembered = Object.prototype.hasOwnProperty.call(memory.columns || {}, column) && memory.columns[column];
    if (remembered && ROLES.includes(remembered.role)) return { role: remembered.role, key: remembered.key || '' };
    const known = Object.prototype.hasOwnProperty.call(DICTIONARY, column.toLowerCase()) && DICTIONARY[column.toLowerCase()];
    if (known) return { role: known[0], key: known[1] || '' };
    const samples = parsed.rows.slice(0, 20).map(({ cells }) => cells[index]).filter((v) => v.trim());
    if (samples.length && samples.filter((v) => validEmail(cleanEmail(v))).length > samples.length / 2) return { role: 'email', key: '' };
    const key = column.replace(/([a-z0-9])([A-Z])/g, '$1_$2').toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '');
    return { role: 'attribute', key: /^[\x20-\x7e]+$/.test(column) && NEW_KEY.test(key) ? key : '' };
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

export function convertCSV(parsed, mapping, existingKeys = []) {
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
      if (examples[issue].length < 5) examples[issue].push({ row, email: maskEmail(email) });
      return;
    }
    seen.add(email);
    const attributes = Object.create(null);
    mapping.forEach(({ role, key }, index) => {
      const value = cells[index].trim();
      if (!value || (role !== 'attribute' && role !== 'tags')) return;
      attributes[key] = role === 'tags' ? [...new Set(value.split(',').map((tag) => tag.trim()).filter(Boolean))] : value;
      if (role === 'tags' && !attributes[key].length) delete attributes[key];
    });
    const name = nameIndex < 0 ? '' : cells[nameIndex].trim();
    const serialized = Object.keys(attributes).length ? JSON.stringify(attributes) : '';
    output.push([email, name, serialized]);
    sourceRows.push(row);
    if (preview.length < 5) {
      preview.push({
        row, email: maskEmail(email), name: maskSample(name), attributes: maskSample(serialized),
      });
    }
  });
  const csv = Papa.unparse({ fields: ['email', 'name', 'attributes'], data: output }, { delimiter: ',', newline: '\r\n' });
  const bytes = new TextEncoder().encode(csv);
  if (bytes.byteLength > LIMITS.output) throw new ImportError('outputLimit');
  summary.imported = output.length;
  return {
    bytes, summary, examples, preview, sourceRows,
  };
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
