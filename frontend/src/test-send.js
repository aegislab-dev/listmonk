// Helpers of the campaign test box (LST-41). The server decides who may receive a test and
// checks every sample; what is here only tells the user early what to expect.

export const TEST_MAX_RECIPIENTS = 10;
export const SAMPLE_MAX_NAME = 200;
export const SAMPLE_MAX_ATTRIBS = 8 * 1024;

export const SAMPLE_DEFAULT = 'default';
export const SAMPLE_SUBSCRIBER = 'subscriber';
export const SAMPLE_CUSTOM = 'custom';

export const cleanEmail = (email) => String(email || '').trim().toLowerCase();

// ownAddress is the address of the signed-in user that may receive a test without being a
// subscriber, or ''. A password account can edit its address freely, so the server does not
// take it as proof of anything; only an account of the identity provider has one.
export function ownAddress(profile) {
  const p = profile || {};
  if (p.type === 'api' || p.passwordLogin || p.password_login) return '';
  return cleanEmail(p.email);
}

// addressKind says how an address is expected to receive a test: 'own' (the signed-in user's
// own address), 'internal' (a test domain, exact match) or 'check' (the server has to find a
// subscriber the user may see).
export function addressKind(email, ownEmail, domains) {
  const e = cleanEmail(email);
  if (e && e === cleanEmail(ownEmail)) return 'own';
  const at = e.lastIndexOf('@');
  if (at > 0 && (domains || []).some((d) => cleanEmail(d) === e.slice(at + 1))) return 'internal';
  return 'check';
}

// newSample is the sample of a test box that nobody has touched: the default one.
export function newSample() {
  return {
    mode: SAMPLE_DEFAULT, subscriber: null, name: '', attribs: '',
  };
}

// attribsTemplate is what the "typed in" attributes start as: every standard key, empty.
export function attribsTemplate(standardAttribs) {
  const out = {};
  (standardAttribs || []).forEach((a) => {
    if (a && a.key) out[a.key] = '';
  });
  return JSON.stringify(out, null, 2);
}

// samplePayload turns the sample of the test box into what the server takes as "sample".
// It returns { payload } (null for the default sample) or { error } with the i18n key of
// what is wrong.
export function samplePayload(sample) {
  const s = sample || newSample();
  if (s.mode === SAMPLE_SUBSCRIBER) {
    if (!s.subscriber || !s.subscriber.id) return { error: 'campaigns.testSend.pickSubscriber' };
    return { payload: { subscriber_id: s.subscriber.id } };
  }
  if (s.mode !== SAMPLE_CUSTOM) return { payload: null };

  const name = String(s.name || '').trim();
  const raw = String(s.attribs || '').trim();
  if (Array.from(name).length > SAMPLE_MAX_NAME) return { error: 'campaigns.testSend.nameTooLong' };
  let attribs = {};
  if (raw) {
    try {
      attribs = JSON.parse(raw);
    } catch (e) {
      return { error: 'campaigns.testSend.attribsInvalid' };
    }
    if (attribs === null || typeof attribs !== 'object' || Array.isArray(attribs)) {
      return { error: 'campaigns.testSend.attribsInvalid' };
    }
    // Keys that were left empty in the template are not sent.
    Object.keys(attribs).forEach((k) => {
      if (attribs[k] === '') delete attribs[k];
    });
    if (new TextEncoder().encode(JSON.stringify(attribs)).length > SAMPLE_MAX_ATTRIBS) {
      return { error: 'campaigns.testSend.attribsTooLong' };
    }
  }
  // Typed in means exactly these values, empty ones too: a message with no name and no
  // attributes is something to test as well. (attribs is always sent, so that the server
  // does not take an empty sample for the default one.)
  return { payload: { name, attribs } };
}

// sampleLabel names the sample for the header of the box and of the preview:
// { kind, name } where name may be empty.
export function sampleLabel(sample) {
  const s = sample || newSample();
  if (s.mode === SAMPLE_SUBSCRIBER && s.subscriber) {
    return { kind: SAMPLE_SUBSCRIBER, name: s.subscriber.name || s.subscriber.email || '' };
  }
  if (s.mode === SAMPLE_CUSTOM) {
    const { payload } = samplePayload(s);
    if (payload) return { kind: SAMPLE_CUSTOM, name: payload.name };
  }
  return { kind: SAMPLE_DEFAULT, name: '' };
}

// rejectedOf reads the addresses the server refused from a failed test request.
export function rejectedOf(err) {
  const list = err && err.response && err.response.data && err.response.data.data
    && err.response.data.data.rejected;
  return Array.isArray(list) ? list.map(cleanEmail) : [];
}
