export function parseCommand(line) {
  if (typeof line !== 'string' || line.length > 16000) throw new Error('Command is too long.');
  const args = []; let word = '', quote = '', escaped = false, started = false;
  for (const c of line.trim()) {
    if (escaped) { word += c; escaped = false; started = true; continue; }
    if (c === '\\' && quote !== "'") { escaped = true; started = true; continue; }
    if (quote) { if (c === quote) quote = ''; else word += c; started = true; continue; }
    if (c === '"' || c === "'") { quote = c; started = true; continue; }
    if ('|;&<>`'.includes(c) || c === '$' || /[\r\n\0]/.test(c)) throw new Error('Enter one Laevitas command. Shell operators and scripts are not supported.');
    if (/\s/.test(c)) { if (started) { args.push(word); word = ''; started = false; } }
    else { word += c; started = true; }
  }
  if (escaped || quote) throw new Error('Close the quoted argument before running.');
  if (started) args.push(word);
  if (args[0] === 'laevitas') args.shift();
  return validateArgs(args);
}

export function validateArgs(args) {
  if (!Array.isArray(args) || args.length > 100 || args.some(x => typeof x !== 'string' || x.length > 4096 || /[\r\n\0]/.test(x))) throw new Error('Use an array of plain command arguments.');
  if (args.some(x => /^(?:0x)?[a-f0-9]{64}$/i.test(x)) || args.some(x => /(?:private.?key|seed.?phrase|wallet.?key)/i.test(x))) throw new Error('Keep private keys and seed phrases in your wallet. Use Connect wallet.');
  if (['config', 'wallet', 'update', 'dash', 'ws', 'watch'].includes(args[0]) ||
      (args[0] === 'agent' && ['pay', 'mcp', 'partner'].includes(args[1]))) throw new Error('This command needs the installed CLI: https://cli.laevitas.ch. The browser uses existing credits and never signs payments.');
  if (args.length && !args[0].startsWith('-') && !['agent','analytics','futures','perps','options','spot','predictions','pred','pm','instruments','commands','doctor','version','help'].includes(args[0])) throw new Error('Unknown browser command. Run --help to see available commands.');
  return args;
}

export function safeOutput(value) {
  // Output is always inserted as text, never HTML. Remove terminal control codes.
  return String(value).replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, '').replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '').replace(/[\x00-\x08\x0b-\x1f\x7f]/g, '').slice(0, 1024 * 1024);
}
