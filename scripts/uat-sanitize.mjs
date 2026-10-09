// Evidence redaction. Secret values come from credential-shaped environment
// variable names and explicit registration; no host auth store is ever read.
const SECRET_NAME = /key|secret|token|password|authorization/i;

export function sanitizer(env = process.env) {
  const secrets = new Set();
  const add = value => { if (typeof value === 'string' && value.length > 3) secrets.add(value); };
  for (const [name, value] of Object.entries(env)) if (SECRET_NAME.test(name)) add(value);
  const redact = value => {
    let text = String(value);
    const forms = new Set();
    for (const secret of secrets) {
      for (let form of [secret, encodeURIComponent(secret)]) {
        // JSON embedded in JSON (tool stdout inside an envelope) adds another
        // escaping layer. Bound expansion by the input length, not a guessed depth.
        while (form.length <= text.length && !forms.has(form)) {
          forms.add(form);
          const escaped = JSON.stringify(form).slice(1, -1);
          if (escaped === form) break;
          form = escaped;
        }
      }
    }
    for (const form of [...forms].sort((a, b) => b.length - a.length)) text = text.split(form).join('[REDACTED]');
    return text.replace(/([?&](?:token|api_key|key)=)[^&\s"<>]+/gi, '$1[REDACTED]')
      .replace(/(Bearer\s+)[\w.+/~=-]+/gi, '$1[REDACTED]')
      .replace(/("(?:token|apiKey|api_key|authorization|password|secret)"\s*:\s*)"(?:\\.|[^"\\])*"/gi, '$1"[REDACTED]"');
  };
  return { redact, add };
}
