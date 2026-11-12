/* global globalThis */
import {
  decodeCSV, parseCSV, suggestMapping, validateMapping, convertCSV,
  inspectCSVHeader, inspectOriginalCSV,
} from './csv-import';

// Subscriber cells stay here. Only the first rows' samples, review results and
// the final upload bytes cross the Worker boundary. Discarding the file terminates us.
let parsed;
function samples() {
  return parsed.columns.map((_, index) => parsed.rows.slice(0, 3).map(({ cells }) => cells[index]));
}

globalThis.onmessage = async ({ data }) => {
  const { id, type } = data;
  try {
    if (type === 'inspectOriginal') {
      const source = await inspectOriginalCSV(data.file, data.delimiter);
      globalThis.postMessage({ id, type: 'source', ...source });
      globalThis.postMessage({ id, type: 'error', code: 'inputLimit' });
    } else if (type === 'parse') {
      const decoded = decodeCSV(data.bytes, data.encoding);
      globalThis.postMessage({
        id, type: 'source', encoding: decoded.encoding, ...inspectCSVHeader(decoded.text, data.delimiter),
      });
      parsed = parseCSV(decoded.text, { delimiter: data.delimiter, header: data.header });
      const mapping = suggestMapping(parsed, data.memory);
      globalThis.postMessage({
        id,
        type,
        encoding: decoded.encoding,
        delimiter: parsed.delimiter,
        columns: parsed.columns,
        total: parsed.total,
        standard: parsed.standard,
        headerHint: parsed.headerHint,
        mapping,
        samples: samples(),
      });
    } else if (type === 'convert' && parsed) {
      const validation = validateMapping(data.mapping, data.keys);
      const review = {
        id, type, validation,
      };
      if (validation.length) {
        globalThis.postMessage(review);
        return;
      }
      const { bytes, ...result } = convertCSV(parsed, data.mapping, data.keys);
      globalThis.postMessage({ ...review, ...result, bytes: bytes.buffer }, [bytes.buffer]);
    }
  } catch (error) {
    globalThis.postMessage({
      id, type: 'error', code: error.code || 'worker', total: error.total,
    });
  }
};
