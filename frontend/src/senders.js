// This is only a display hint. The server parses and authorizes every address.
export function senderMailbox(value) {
  const s = (value || '').trim();
  if (!s || /[\r\n]/.test(s)) return '';
  const match = s.match(/^(?:[^<>]*)<([^<>]+)>$/);
  const email = match ? match[1] : s;
  return /^[^\s<>@,;]+@[^\s<>@,;]+$/.test(email) ? email.toLowerCase() : '';
}

export function isSenderDomain(value, domains) {
  const email = senderMailbox(value);
  return !!email && (domains || []).includes(email.slice(email.lastIndexOf('@') + 1));
}
