// This is only a display hint. The server parses and authorizes every address.
export function senderMailbox(value) {
  const s = (value || '').trim();
  if (!s || /[\r\n]/.test(s)) return '';
  const match = s.match(/^(?:[^<>]*)<([^<>]+)>$/);
  const email = match ? match[1] : s;
  return /^[^\s<>@,;]+@[^\s<>@,;]+$/.test(email) ? email.toLowerCase() : '';
}

// The address of a From value, with a quoted local part ("two words"@x) read
// as the address without quotes, as the server does. Used to compare a From
// with the From list.
export function fromAddress(value) {
  const s = (value || '').trim();
  if (!s || /[\r\n]/.test(s)) return '';
  const match = s.match(/^(?:[^<>]*)<([^<>]+)>$/);
  const email = match ? match[1] : s;
  const parts = email.match(/^(?:"((?:[^"\\]|\\.)+)"|([^\s<>@,;"]+))@([^\s<>@,;"]+)$/);
  if (!parts) return '';
  const local = parts[1] !== undefined ? parts[1].replace(/\\(.)/g, '$1') : parts[2];
  return `${local}@${parts[3]}`.toLowerCase();
}

export function isSenderDomain(value, domains) {
  const email = senderMailbox(value);
  return !!email && (domains || []).includes(email.slice(email.lastIndexOf('@') + 1));
}

// The list entry a From value stands for: the same entry, or the first entry
// with the same address. Display hint only; the server decides.
export function matchFromEntry(value, list) {
  const v = (value || '').trim();
  if (!v) return '';
  if ((list || []).includes(v)) return v;
  const email = fromAddress(v);
  return email ? ((list || []).find((e) => fromAddress(e) === email) || '') : '';
}

// Mirrors the server's placeholder rule (change-me@..., reserved example domains).
export function isPlaceholderFrom(value) {
  const email = fromAddress(value);
  if (!email) return false;
  const at = email.lastIndexOf('@');
  const local = email.slice(0, at);
  const domain = email.slice(at + 1);
  if (['change-me', 'changeme', 'change_me'].includes(local)) return true;
  if (['example.com', 'example.org', 'example.net'].some((d) => domain === d || domain.endsWith(`.${d}`))) return true;
  return ['example', 'invalid', 'test'].some((t) => domain.endsWith(`.${t}`));
}
