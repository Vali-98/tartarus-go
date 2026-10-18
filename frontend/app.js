'use strict';

const api = () => window.go.main.App;
const T = { patrol: 3, devour: 4, chain: 5, swap: 8, hunt: 9 };
const KEY = 'tartarus.board.v1';

let meta = null;
let board = null;
let selected = 1;
let advice = null;
let summary = null;
let jobId = 0;
let settings = { clickThrough: false, collapsed: false };
let game = { running: false, foreground: false };
let logStatus = { state: 'searching', message: 'Locating UE_game.log...' };
let logPathDraft = null;
let runEnded = false;
let treasureStep = '';
const lastEvent = {};
let solveTimer = 0;
let scanWorker = null;
let scanId = 0;
let overlayDragging = false;
let overlayHeight = 0;
let scanInfo = { status: 'No screenshot read yet.', image: '', checks: [], notes: [] };

// ---------- DOM helpers ----------

function h(tag, props, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (k === 'class') e.className = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (k in e) e[k] = v;
    else e.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    e.append(kid.nodeType ? kid : document.createTextNode(kid));
  }
  return e;
}

function select(options, value, onchange) {
  const s = h('select', { onchange: (e) => onchange(e.target.value) }, options.map(([v, label]) => h('option', { value: v, selected: String(v) === String(value) }, label)));
  return s;
}

const num = (value, min, max, onchange) => h('input', { type: 'number', min, max, value, onchange: (e) => onchange(e.target.value === '' ? '' : Number(e.target.value)) });
const check = (value, onchange) => h('input', { type: 'checkbox', checked: !!value, onchange: (e) => onchange(e.target.checked) });
const row = (label, ...ctl) => h('div', { class: 'row' }, h('label', {}, label), ...ctl);

// ---------- Board ----------

function blankBoard() {
  const cells = Array.from({ length: 16 }, (_, n) => ({ kind: n === 0 ? 'empty' : 'sealed', beast: 'Mammoth', tier: 0, event: '', known: 0, atLeast: 0, scratch: false, ivory: false, nestDue: 0 }));
  return { cells, warden: 0, day: 1, maxDays: 0, phase: 'patrol', dest: [], tools: [], vault: 0, objective: 'king', progress: 0, milestone: false, startEvent: '', chain: false, chainSelect: false, chainTargets: [], usedSwap: false, usedDevour: false, noSwap: false, noDevour: false, devourAfterSwap: false, eventKind: '', pending: '', offer: [], giftPicks: 1, rerolls: 0, freshOffer: false };
}

function loadBoard() {
  try {
    const b = JSON.parse(localStorage.getItem(KEY));
    if (b && Array.isArray(b.cells) && b.cells.length === 16) return Object.assign(blankBoard(), b);
  } catch (e) { /* start blank */ }
  return blankBoard();
}

function changed(now) {
  localStorage.setItem(KEY, JSON.stringify(board));
  render();
  clearTimeout(solveTimer);
  solveTimer = setTimeout(solve, now ? 0 : 250);
}

function payload() {
  const b = JSON.parse(JSON.stringify(board));
  b.cells.forEach((c) => { c.beast = c.beast.toLowerCase(); });
  b.maxDays = Number(b.maxDays) || 0;
  return b;
}

function readScreenshot(file) {
  if (!file || !file.type.startsWith('image/')) return;
  if (scanInfo.image.startsWith('blob:')) URL.revokeObjectURL(scanInfo.image);
  scanInfo.status = 'Reading screenshot...';
  scanInfo.image = URL.createObjectURL(file);
  scanInfo.checks = [];
  scanInfo.notes = [];
  render();
  processImage(file, 0);
}

function processImage(blob, frameID, kind = '') {
  createImageBitmap(blob).then((bitmap) => {
    const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
    const context = canvas.getContext('2d', { willReadFrequently: true });
    context.drawImage(bitmap, 0, 0);
    bitmap.close();
    const pixels = context.getImageData(0, 0, canvas.width, canvas.height);
    const id = ++scanId;
    if (!scanWorker) {
      scanWorker = new Worker('scan-worker.js');
      scanWorker.onmessage = (event) => {
        try {
          if (event.data.id !== scanId) return;
          if (event.data.error) {
            scanInfo.status = event.data.error;
            render();
            return;
          }
          if (event.data.kind === 'instruments') applyPick(event.data.result);
          else applyScan(event.data.result, event.data.kind);
        } finally {
          if (event.data.frameID) api().ScanComplete(event.data.frameID);
        }
      };
    }
    scanWorker.postMessage({ id, frameID, kind, width: canvas.width, height: canvas.height, data: pixels.data.buffer }, [pixels.data.buffer]);
  }).catch((error) => {
    if (frameID) api().ScanComplete(frameID);
    scanInfo.status = `Couldn't read image: ${error.message || error}`;
    render();
  });
}

function readLiveFrame(frame) {
  if ((!settings.autoScan && frame && frame.kind !== 'manual') || !frame || !frame.jpg) {
    if (frame && frame.id) api().ScanComplete(frame.id);
    return;
  }
  const binary = atob(frame.jpg);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  scanInfo.status = frame.kind && frame.kind !== 'foreground' ? `Live log trigger: ${frame.kind}` : 'Live foreground scan';
  render();
  processImage(new Blob([bytes], { type: 'image/jpeg' }), frame.id, frame.kind);
}

function applyPick(result) {
  if (!result || !result.ok || !result.cards || result.cards.some((card) => !card || !card.sim)) {
    scanInfo.status = result && result.reason ? result.reason : 'Could not read all three instrument cards.';
    scanInfo.checks = [{ label: 'Instrument offer needs a clearer screenshot', conf: 0, cell: -1 }];
    render();
    return;
  }
  const old = board;
  const fresh = !!old.freshOffer || (old.maxDays > 0 && old.day >= old.maxDays) || (old.maxDays <= 0 && old.day <= 1 && old.tools.length === 0);
  const next = fresh ? blankBoard() : JSON.parse(JSON.stringify(old));
  const offers = result.cards.map((card) => card.sim);
  next.day = fresh ? 1 : old.day;
  next.maxDays = fresh ? (old.maxDays || 20) : old.maxDays;
  next.phase = fresh ? 'choice' : (old.phase === 'choice' ? 'manage' : old.phase);
  next.pending = 'tool';
  next.offer = offers;
  next.rerolls = result.reroll ? 1 : 0;
  next.freshOffer = fresh;
  if (fresh) next.startEvent = '';
  board = next;
  treasureStep = '';
  runEnded = false;
  selected = next.warden;
  const names = result.cards.map((card) => meta.tools.find((tool) => tool.id === card.sim)?.name || card.sim);
  const unsure = result.cards.map((card, i) => card.conf < 0.5 ? `${['left', 'middle', 'right'][i]} card (${names[i]}, ${Math.round(card.conf * 100)}%)` : '').filter(Boolean);
  scanInfo.status = `${fresh ? 'Start-of-run' : 'Reward'} instrument offer: ${names.join(', ')}${result.reroll ? ' (re-roll available)' : ''}.`;
  scanInfo.checks = unsure.map((label) => ({ label: `Verify ${label}`, conf: 0, cell: -1 }));
  localStorage.setItem(KEY, JSON.stringify(board));
  summary = null;
  advice = null;
  render();
  clearTimeout(solveTimer);
  solveTimer = setTimeout(solve, 0);
}

function applyTreasureChoice() {
  treasureStep = '';
  const next = JSON.parse(JSON.stringify(board));
  next.phase = 'choice';
  next.pending = 'gift';
  next.offer = meta.gifts.map((gift) => gift.id);
  next.giftPicks = next.startEvent === 'opulence' ? 3 : 1;
  next.rerolls = 0;
  next.freshOffer = false;
  board = next;
  scanInfo.status = `Warden's Treasure: choose a gift${next.giftPicks > 1 ? ` (${next.giftPicks} picks)` : ''}.`;
  scanInfo.checks = [];
  localStorage.setItem(KEY, JSON.stringify(board));
  summary = null;
  advice = null;
  render();
  clearTimeout(solveTimer);
  solveTimer = setTimeout(solve, 0);
}

function applyScan(result, frameKind = '') {
  if (frameKind === 'run-end') {
    treasureStep = '';
    runEnded = true;
    summary = null;
    advice = null;
    scanInfo.status = 'Run complete. Waiting for the next run.';
    scanInfo.checks = [];
    render();
    return;
  }
  if (!result.ok) {
    if (frameKind === 'treasure') {
      treasureStep = 'view';
      scanInfo.status = "Warden's Treasure: select View Prison Status in the game to rescan the board.";
      scanInfo.checks = [];
      render();
      return;
    }
    if (treasureStep === 'view' && frameKind === 'board') {
      treasureStep = 'return';
      scanInfo.status = 'Treasure status changed. Click Return to Selection in the game to continue.';
      scanInfo.checks = [];
      render();
      return;
    }
    if (treasureStep === 'return') {
      applyTreasureChoice();
      return;
    }
    scanInfo.status = result.reason;
    scanInfo.checks = [];
    scanInfo.notes = [];
    if (frameKind) {
      summary = null;
      advice = { error: 'Waiting for the current game controls to be readable.' };
    }
    render();
    return;
  }
  const previous = JSON.stringify(board);
  const old = board;
  const next = JSON.parse(JSON.stringify(old));
  const checks = [];
  const addCheck = (label, conf, cell = -1) => {
    if (conf != null && conf < 0.6) checks.push({ label, conf, cell });
  };
  result.cells.forEach((read, n) => {
    if (n === 0) return;
    const was = old.cells[n];
    const event = read.event ? 'unknown' : '';
    if (read.kind === 'locked') {
      if (read.nest) {
        next.cells[n] = { kind: 'nest', beast: 'Rat', tier: 0, event: '', known: 0, atLeast: 0, scratch: false, ivory: false, nestDue: next.day + 2 };
      } else if (read.shown) {
        next.cells[n] = { kind: 'sealed', beast: 'Mammoth', tier: read.shown.tier, event, known: 1, atLeast: 0, scratch: !!read.shown.scratch, ivory: false, nestDue: 0 };
        addCheck(`Cell ${String(n + 1).padStart(2, '0')} rarity`, read.shown.conf, n);
      } else {
        next.cells[n] = { kind: 'sealed', beast: 'Mammoth', tier: 0, event, known: 0, atLeast: was.atLeast || 0, scratch: was.scratch || false, ivory: false, nestDue: 0 };
      }
    } else if (read.kind === 'inmate') {
      const beast = read.beast[0].toUpperCase() + read.beast.slice(1);
      next.cells[n] = { kind: 'open', beast, tier: read.tier, event, known: 0, atLeast: 0, scratch: false, ivory: !!(was.kind === 'open' && was.ivory && was.beast === beast && read.tier >= was.tier), nestDue: 0 };
      addCheck(`Cell ${String(n + 1).padStart(2, '0')} inmate`, read.conf, n);
      if (read.ghost) addCheck(`Cell ${String(n + 1).padStart(2, '0')} portrait is washed out`, 0.4, n);
    } else {
      next.cells[n] = { kind: 'empty', beast: 'Mammoth', tier: 0, event: '', known: 0, atLeast: 0, scratch: false, ivory: false, nestDue: 0 };
      addCheck(`Cell ${String(n + 1).padStart(2, '0')} looks empty`, read.conf, n);
    }
  });

  if (result.warden) {
    next.warden = result.warden.cell;
    addCheck('Warden position', result.warden.conf, result.warden.cell);
  } else checks.push({ label: 'Warden position not found', conf: 0, cell: -1 });

  if (result.days) {
    let maxDays = next.maxDays || 20;
    if (next.startEvent === 'ironlaw' || result.badge && result.badge.id === 'ironlaw') maxDays = 30;
    const daysLeft = result.days.days;
    if (daysLeft < maxDays) {
      next.maxDays = maxDays;
      next.day = Math.max(1, maxDays - daysLeft);
    } else if (daysLeft < 30) {
      next.maxDays = 30;
      next.day = Math.max(1, 30 - daysLeft);
      checks.push({ label: `Riot countdown suggests a 30-day Iron Law run (${daysLeft} days remain); verify the day`, conf: 0, cell: -1 });
    } else {
      checks.push({ label: `Riot countdown read as ${daysLeft} days, outside the supported run lengths; verify the day`, conf: 0, cell: -1 });
    }
    addCheck('Days until riot', result.days.conf);
  } else checks.push({ label: 'Days until riot not found', conf: 0, cell: -1 });

  if (result.doors && result.doors.cells.length) {
    next.phase = 'patrol';
    next.dest = result.doors.cells.filter((n) => n !== next.warden);
    next.usedSwap = false;
    next.usedDevour = false;
    next.noSwap = false;
    next.noDevour = false;
    next.devourAfterSwap = false;
    addCheck('Patrol cards', result.doors.conf);
    if (result.doors.hidden && result.doors.hidden.length) checks.push({ label: `Card number obscured at cell ${result.doors.hidden.map((n) => String(n + 1).padStart(2, '0')).join(', ')}`, conf: 0, cell: result.doors.hidden[0] });
  } else if (result.decision) {
    next.phase = result.decision.office ? 'office' : 'manage';
    next.dest = [];
    next.usedSwap = false;
    next.usedDevour = false;
  } else {
    next.phase = old.phase === 'patrol' ? 'manage' : old.phase;
    next.dest = old.dest;
  }
  if (result.decision && !result.decision.office) {
    next.usedSwap = false;
    next.usedDevour = false;
    next.noSwap = !result.decision.replace;
    next.noDevour = !result.decision.consume;
    next.devourAfterSwap = next.noSwap && result.decision.consume;
    if (next.noSwap && result.decision.consume) {
      next.usedSwap = true;
    }
  }
  next.chain = false;
  next.chainSelect = false;
  next.chainTargets = [];
  next.pending = '';
  next.offer = [];
  next.giftPicks = 1;
  next.freshOffer = false;

  if (result.tools && result.tools.tools) {
    const read = result.tools.tools.filter((tool) => tool.conf >= 0.6).map((tool) => tool.id);
    if (read.length) next.tools = [...new Set([...next.tools, ...read])];
    result.tools.tools.forEach((tool) => addCheck(`Instrument: ${tool.id}`, tool.conf));
    if (!result.tools.found) checks.push({ label: 'Instrument panel not found; previous tools kept', conf: 0, cell: -1 });
  }
  if (result.chromatic) next.vault = result.chromatic.count;
  if (result.badge && result.badge.id) next.startEvent = result.badge.id;
  if (result.chain) {
    next.phase = 'manage';
    next.usedDevour = true;
    next.chainSelect = true;
    next.chainTargets = result.chain.targets.slice();
    const eater = next.cells[next.warden];
    result.chain.targets.forEach((n) => {
      const meal = next.cells[n];
      if (meal.kind === 'open') meal.tier = eater.tier;
      else if (meal.kind === 'sealed' && meal.scratch) {
        meal.tier = eater.tier;
        meal.known = 1;
      } else checks.push({ label: `Consume target in cell ${String(n + 1).padStart(2, '0')} needs a check`, conf: 0, cell: n });
    });
    checks.push({ label: 'Consume selection screen: verify other darkened cells', conf: 0, cell: next.warden });
  }
  const returnToSelection = !!(result.chromatic && result.chromatic.returnToSelection);
  checks.push({ label: 'Verify the objective; it is not read automatically', conf: 0, cell: -1 });
  if (result.notes) result.notes.forEach((note) => checks.push({ label: note, conf: 0, cell: -1 }));

  board = next;
  const stateChanged = previous !== JSON.stringify(next);
  const live = scanInfo.status.startsWith('Live') || scanInfo.status.startsWith('Reading foreground');
  const controlsReady = !!result.chain || !!(result.doors && result.doors.cells.length) || !!result.decision;
  scanInfo.status = `${live ? 'Live' : 'Board'} read in ${result.ms} ms${result.loc ? ` (match ${(result.loc.score || 0).toFixed(2)})` : ''}. Review ${checks.length} item${checks.length === 1 ? '' : 's'}.${stateChanged ? '' : ' Board unchanged.'}`;
  scanInfo.checks = checks;
  scanInfo.notes = result.notes || [];
  localStorage.setItem(KEY, JSON.stringify(board));
  selected = result.warden ? result.warden.cell : selected;
  if (returnToSelection || treasureStep === 'view' && frameKind === 'board') {
    treasureStep = 'return';
    scanInfo.status = returnToSelection
      ? "Board rescanned. Click Return to Selection in the game to continue."
      : 'Board updated. Click Return to Selection in the game to continue.';
    summary = null;
    advice = null;
    render();
    return;
  }
  if (treasureStep === 'return') {
    applyTreasureChoice();
    return;
  }
  if (frameKind === 'treasure') {
    treasureStep = 'view';
    scanInfo.status = "Warden's Treasure: select View Prison Status in the game to rescan the board.";
    summary = null;
    advice = null;
    render();
    return;
  }
  if (treasureStep === 'view') {
    scanInfo.status = "Warden's Treasure: select View Prison Status in the game to rescan the board.";
    summary = null;
    advice = null;
    render();
    return;
  }
  render();
  if (controlsReady && (stateChanged || advice && advice.error)) {
    summary = null;
    advice = null;
    clearTimeout(solveTimer);
    solveTimer = setTimeout(solve, 0);
  } else if (!controlsReady) {
    summary = null;
    advice = { error: 'Waiting for the game’s current choice controls.' };
    render();
  }
}

async function solve() {
  summary = null;
  advice = null;
  render();
  try {
    const adv = await api().Solve(payload());
    advice = adv;
    jobId = adv.jobId;
    if (lastEvent[jobId]) summary = lastEvent[jobId].summary;
  } catch (e) {
    advice = { error: String(e) };
  }
  render();
}

// ---------- Rendering ----------

function ringPos(n) {
  if (n <= 5) return [1, n + 1];
  if (n <= 7) return [n - 4, 6];
  if (n <= 13) return [4, 6 - (n - 8)];
  return [n === 14 ? 3 : 2, 1];
}

function recommended() {
  const a = summary && summary.options.length ? summary.options[0].action : advice && !advice.error ? advice.action : null;
  if (!a) return -1;
  if ([T.patrol, T.devour, T.chain, T.hunt].includes(a.type)) return a.cell;
  if (a.type === T.swap) return board.warden;
  return -1;
}

function cellView(n) {
  const c = board.cells[n];
  const tier = (t) => meta.tiers[t];
  let body, sub = '', cls = 'cell';
  if (n === 0) body = h('div', { class: 'body' }, 'Office');
  else if (c.kind === 'open') {
    body = h('div', { class: 'body tier' + c.tier }, `${tier(c.tier)} ${c.beast}`);
    sub = c.ivory ? 'Tusk' : '';
  } else if (c.kind === 'empty') body = h('div', { class: 'body' }, 'Empty');
  else if (c.kind === 'nest') body = h('div', { class: 'body' }, 'Rat nest');
  else {
    cls += ' sealed';
    body = h('div', { class: 'body' + (c.known ? ' tier' + c.tier : '') }, c.known === 2 ? `${tier(c.tier)} ${c.beast}` : c.known === 1 ? `${tier(c.tier)}` : 'Locked');
    sub = [c.scratch ? 'scratched' : '', c.atLeast ? `${tier(c.atLeast)}+` : ''].filter(Boolean).join(' ');
  }
  const ev = c.event && n !== 0 ? (c.event === 'unknown' ? '!' : '! ' + (meta.events.find((e) => e.id === c.event) || { name: c.event }).name) : '';
  if (n === selected) cls += ' sel';
  if (n === recommended()) cls += ' rec';
  if (scanInfo.checks.some((item) => item.cell === n)) cls += ' needs-check';
  return h('div', { class: cls, style: `grid-row:${ringPos(n)[0]};grid-column:${ringPos(n)[1]}`, onclick: () => { selected = n; render(); } },
    h('span', { class: 'n' }, String(n + 1).padStart(2, '0')),
    h('div', { class: 'tags' },
      n === board.warden && h('span', { class: 'tag w' }, 'W'),
      board.dest.includes(n) && h('span', { class: 'tag d' }, 'D'),
      ev && h('span', { class: 'tag e', title: ev }, '!')),
    body,
    h('div', { class: 'sub' }, [sub, ev.length > 1 ? ev.slice(2) : ''].filter(Boolean).join(' ')));
}

function centerView() {
  const best = summary && summary.options[0];
  const label = best ? best.label : advice && !advice.error ? advice.label : '';
  return h('div', { class: 'center', style: 'grid-row:2/4;grid-column:2/6' },
    h('div', { class: 'stat' }, `Day ${board.day}${board.maxDays ? ' of ' + board.maxDays : ''}, ${board.phase}, vault ${board.vault}/10`),
    h('div', { class: 'big' }, label || (advice && advice.error ? '' : 'Solving...')),
    advice && advice.error && h('div', { class: 'err' }, advice.error),
    advice && advice.why && h('div', { class: 'why' }, advice.why));
}

function resultView() {
  const box = h('div', { class: 'result' }, h('h3', {}, 'Live check'));
  if (advice && advice.error) {
    box.append(h('div', { class: 'err' }, advice.error));
    return box;
  }
  if (advice) {
    box.append(h('div', { class: 'stat' }, 'Strategy pick'), h('div', { class: 'rec-line' }, advice.label), h('div', { class: 'why' }, advice.why));
  }
  if (summary) {
    const top = Math.max(0.0001, ...summary.options.map((o) => o.mean));
    summary.options.forEach((o, i) => box.append(h('div', { class: 'opt' + (i === 0 ? ' top' : '') },
      h('span', { class: 'lab' }, (o.policy ? '* ' : '') + o.label), h('span', { class: 'num' }, o.mean.toFixed(3)), h('span', { class: 'se' }, '±' + o.se.toFixed(3)),
      h('i', { class: 'bar', style: `width:${Math.max(0, (o.mean / top) * 100)}%` }))));
    box.append(h('div', { class: 'progress' }, h('i', { style: `width:${Math.min(100, (summary.rounds / summary.target) * 100)}%` })),
      h('div', { class: 'stat' }, `${summary.rounds} playouts per option${summary.done ? ', done' : ''}. Value: Rainbows vaulted by the end of the run. * strategy pick.`));
  }
  return box;
}

function cellEditor(open = true) {
  const c = board.cells[selected];
  const box = h('div', { class: 'panel-content' });
  const set = (k, v) => { c[k] = v; changed(); };
  if (selected === 0) {
    box.append(h('div', { class: 'stat' }, "The warden's office. It never holds anyone."));
  } else {
    box.append(row('State', select([['sealed', 'Locked'], ['open', 'Inmate'], ['empty', 'Empty open cell'], ['nest', 'Rat nest']], c.kind, (v) => set('kind', v))));
    const beasts = meta.beasts.map((b) => [b.name, b.name]);
    const tiers = meta.tiers.slice(0, 5).map((t, i) => [i, t]);
    if (c.kind === 'open') {
      box.append(row('Rarity', select(tiers, c.tier, (v) => set('tier', +v))), row('Beast', select(beasts, c.beast, (v) => set('beast', v))), row('Tusk mark', check(c.ivory, (v) => set('ivory', v))));
    } else if (c.kind === 'sealed') {
      box.append(row('Shows', select([[0, 'Nothing'], [1, 'Rarity'], [2, 'Rarity and beast']], c.known, (v) => set('known', +v))));
      if (c.known >= 1) box.append(row('Rarity', select(tiers, c.tier, (v) => set('tier', +v))));
      if (c.known === 2) box.append(row('Beast', select(beasts, c.beast, (v) => set('beast', v))));
      box.append(row('At least', select([[0, 'Unknown'], [2, 'Rare (Hunt left it dark)']], c.atLeast, (v) => set('atLeast', +v))), row('Scratched', check(c.scratch, (v) => set('scratch', v))));
    } else if (c.kind === 'nest') {
      box.append(row('Hatches', num(c.nestDue || '', 1, 60, (v) => set('nestDue', v || 0)), h('span', { class: 'stat' }, 'day')));
    }
    if (c.kind !== 'empty') {
      box.append(row('Event', select([['', 'None'], ['unknown', 'Unknown kind'], ...meta.events.map((e) => [e.id, e.name])], c.event, (v) => set('event', v))));
    }
  }
  box.append(h('div', { class: 'row' },
    h('button', { onclick: () => { board.warden = selected; changed(); } }, 'Warden here'),
    selected !== board.warden && h('button', { onclick: () => { const i = board.dest.indexOf(selected); if (i >= 0) board.dest.splice(i, 1); else board.dest.push(selected); changed(); } }, board.dest.includes(selected) ? 'Remove door card' : 'Door card')));
  return h('details', { class: 'side-panel', id: 'cell-editor', open }, h('summary', {}, `Cell ${String(selected + 1).padStart(2, '0')}`), box);
}

function globalsView(open = true) {
  const box = h('div', { class: 'panel-content' });
  const set = (k) => (v) => { board[k] = v; changed(); };
  box.append(
    row('Day', num(board.day, 1, 99, set('day')), h('span', { class: 'stat' }, 'of'), num(board.maxDays || '', 5, 99, (v) => { board.maxDays = v || 0; changed(); })),
    row('Phase', select([['patrol', 'Patrol (pick a door)'], ['manage', 'Manage (consume/replace)'], ['office', 'At the office'], ['event', 'Special event']], board.phase, set('phase'))));
  if (board.phase === 'event') box.append(row('Event', select([['', 'Pick...'], ...meta.events.map((e) => [e.id, e.name])], board.eventKind, set('eventKind'))));
  box.append(
    row('Vault', num(board.vault, 0, 10, set('vault'))),
    row('Objective', select(meta.objectives.map((o) => [o.id, o.name]), board.objective, set('objective'))),
    row('Progress', num(board.progress, 0, 99, set('progress')), check(board.milestone, set('milestone')), h('span', { class: 'stat' }, 'met')),
    row('Start card', select(meta.startCards.map((o) => [o.id, o.name]), board.startEvent, set('startEvent'))),
    row('Today', h('label', {}, check(board.chain, set('chain')), ' chain armed'), h('label', {}, check(board.usedSwap, set('usedSwap')), ' replaced'), h('label', {}, check(board.usedDevour, set('usedDevour')), ' consumed')),
    row('Choice', select([['', 'None'], ['tool', 'Pick a tool'], ['gift', "Warden's Treasure"]], board.pending, (v) => { board.pending = v; board.offer = []; changed(); })));
  if (board.pending) {
    const list = board.pending === 'tool' ? meta.tools : meta.gifts;
    const n = board.pending === 'tool' ? 3 : 4;
    for (let i = 0; i < n; i++) box.append(row(`Offer ${i + 1}`, select([['', '-'], ...list.map((t) => [t.id, t.name])], board.offer[i] || '', (v) => { board.offer[i] = v; board.offer = board.offer.filter(Boolean); changed(); })));
    if (board.pending === 'tool') box.append(row('Re-rolls', num(board.rerolls, 0, 9, set('rerolls'))));
  }
  const filter = h('input', { type: 'text', placeholder: 'Filter tools...' });
  const grid = h('div', { class: 'tools' });
  const fill = () => {
    grid.replaceChildren(...meta.tools.filter((t) => t.name.toLowerCase().includes(filter.value.toLowerCase())).map((t) => h('label', { title: t.name }, check(board.tools.includes(t.id), (v) => { board.tools = v ? [...board.tools, t.id] : board.tools.filter((x) => x !== t.id); changed(); }), h('span', {}, t.name))));
  };
  filter.addEventListener('input', fill);
  fill();
  box.append(h('h3', { style: 'margin-top:10px' }, `Tools held (${board.tools.length})`), filter, grid);
  return h('details', { class: 'side-panel', id: 'globals', open }, h('summary', {}, 'Run & settings'), box);
}

function scanPanel(open = true) {
  const panel = h('div', { class: 'panel-content' }, h('div', { class: 'stat' }, scanInfo.status));
  panel.append(h('div', { class: 'row' },
    h('label', {}, check(settings.autoScan, (value) => saveSettings({ autoScan: value })), ' Follow foreground game'),
    select([['game', 'Game-first'], ['one', 'One worker'], ['quarter', 'Quarter'], ['half', 'Half'], ['most', 'Most']], settings.cpu || 'game', (value) => saveSettings({ cpu: value }))));
  panel.append(h('h3', { style: 'margin-top:10px' }, 'UE game log'),
    h('div', { id: 'log-follow-status', class: 'stat' }, `${logStatus.state || 'searching'}: ${logStatus.message || ''}${logStatus.path ? ` (${logStatus.path})` : ''}`),
    h('div', { class: 'row log-path' },
      h('input', { id: 'log-path', type: 'text', value: logPathDraft === null ? settings.logPath || '' : logPathDraft, placeholder: 'Auto-detect UE_game.log', oninput: (event) => { logPathDraft = event.target.value; } }),
      h('button', { id: 'browse-log', title: 'Browse for UE_game.log' }, 'Browse'),
      h('button', { id: 'save-log-path', title: 'Use this log path' }, 'Use')));
  if (scanInfo.image) {
    const img = h('img', { class: 'scan-preview', src: scanInfo.image, alt: 'Screenshot being read' });
    panel.append(img);
  }
  if (scanInfo.checks.length) {
    const list = h('div', { class: 'scan-checks' });
    scanInfo.checks.forEach((item) => list.append(h('button', { class: 'scan-check', onclick: () => { if (item.cell >= 0) { selected = item.cell; render(); } } },
      h('span', {}, item.label), h('b', {}, item.conf > 0 ? `${Math.round(item.conf * 100)}%` : 'Check'))));
    panel.append(list);
  }
  return h('details', { class: 'side-panel', id: 'scan-panel', open }, h('summary', {}, 'Screenshot reader'), panel);
}

function renderAnalyzer() {
  const ring = document.getElementById('ring');
  ring.replaceChildren(...Array.from({ length: 16 }, (_, n) => cellView(n)), centerView());
  document.getElementById('result').replaceWith(Object.assign(resultView(), { id: 'result' }));
  // Keep focus in the side panel while typing: rebuild only the editors.
  const ed = document.getElementById('cell-editor');
  const gl = document.getElementById('globals');
  const scan = document.getElementById('scan-panel');
  const scanOpen = scan.open === undefined ? true : scan.open;
  const cellOpen = ed.open === undefined ? true : ed.open;
  const globalsOpen = gl.open === undefined ? true : gl.open;
  scan.replaceWith(scanPanel(scanOpen));
  const active = document.activeElement;
  if (!active || !ed.contains(active) && !gl.contains(active) || active.tagName !== 'INPUT' || active.type === 'checkbox') {
    ed.replaceWith(cellEditor(cellOpen));
    gl.replaceWith(globalsView(globalsOpen));
  }
}

async function scanNow() {
  scanInfo.status = 'Requesting a fresh game screenshot...';
  render();
  try {
    const queued = await api().ScanNow();
    scanInfo.status = queued
      ? 'Manual scan queued; the game may briefly come to the foreground.'
      : 'Scan unavailable. Make sure the game is running and its window is accessible.';
  } catch (error) {
    scanInfo.status = `Scan failed: ${error.message || error}`;
  }
  render();
}

function renderOverlay() {
  const body = document.getElementById('ov-body');
  const best = summary && summary.options[0];
  const label = runEnded ? 'Run complete' : best ? best.label : advice && !advice.error ? advice.label : advice && advice.error ? '' : 'Solving...';
  const kids = [];
  if (treasureStep === 'return') kids.push(h('div', { class: 'ov-prompt' }, 'Board rescanned. Click Return to Selection in the game.'));
  else if (treasureStep === 'view') kids.push(h('div', { class: 'ov-prompt' }, 'Select View Prison Status in the game to rescan the board.'));
  if (advice && advice.error) kids.push(h('div', { class: 'ov-why' }, advice.error));
  else {
    kids.push(h('div', { class: 'ov-best' }, label));
    if (advice && advice.why) kids.push(h('div', { class: 'ov-why' }, advice.why));
    if (summary) {
      summary.options.slice(0, 4).forEach((o, i) => kids.push(h('div', { class: 'ov-row' + (i === 0 ? ' top' : '') }, h('span', {}, o.label), h('span', {}, o.mean.toFixed(2)))));
      kids.push(h('div', { class: 'ov-foot' }, `${summary.rounds} playouts${summary.done ? '' : '...'}  Ctrl+Alt+S re-solve, Ctrl+Alt+A analyzer`));
    }
  }
  if (!settings.clickThrough) {
    kids.push(h('button', {
      class: 'ov-scan',
      disabled: !game.running,
      title: 'Capture the current game screen, briefly bringing the game forward if needed',
      onclick: scanNow,
    }, 'Scan now'));
  }
  body.replaceChildren(...kids);
  document.getElementById('ov-dot').className = 'dot ' + (game.running ? 'on' : 'off');
  const lock = document.getElementById('ov-lock');
  lock.textContent = settings.clickThrough ? 'Unlock (Ctrl+Alt+L)' : 'Lock';
  lock.title = settings.clickThrough ? 'Click-through is on. Press Ctrl+Alt+L to unlock.' : 'Enable click-through (Ctrl+Alt+L to toggle).';
  document.body.classList.toggle('collapsed', !!settings.collapsed);
  resizeOverlayToContent();
}

function resizeOverlayToContent() {
  if (!document.body.classList.contains('overlay') || overlayDragging) return;
  const hgt = Math.ceil(document.getElementById('overlay').getBoundingClientRect().height);
  if (Math.abs(hgt - overlayHeight) < 2) return;
  overlayHeight = hgt;
  api().OverlayHeight(hgt);
}

function render() {
  if (!meta) return;
  const g = document.getElementById('game');
  g.className = 'pill ' + (game.running ? 'on' : 'off');
  g.textContent = game.running ? 'game: running' : 'game: not running';
  const rainbows = `${Math.min(10, board.vault || 0)}/10`;
  document.getElementById('rainbow-count').textContent = `Rainbows ${rainbows}`;
  document.getElementById('ov-rainbow-count').textContent = `${rainbows} Rainbows`;
  if (document.body.classList.contains('overlay')) renderOverlay();
  else renderAnalyzer();
}

// ---------- Startup ----------

function setMode(mode) {
  if (mode === 'overlay') overlayHeight = 0;
  document.body.classList.toggle('overlay', mode === 'overlay');
  render();
}

async function saveSettings(patch) {
  settings = Object.assign({}, settings, patch);
  await api().SaveSettings(settings);
  render();
}

async function init() {
  meta = await api().Meta();
  settings = await api().GetSettings();
  game = await api().GameStatus();
  logStatus = await api().GetLogStatus();
  board = loadBoard();
  const rt = window.runtime;
  rt.EventsOn('solve', (ev) => {
    lastEvent[ev.jobId] = ev;
    for (const k of Object.keys(lastEvent)) if (+k < ev.jobId - 2) delete lastEvent[k];
    if (ev.jobId === jobId) { summary = ev.summary; render(); }
  });
  rt.EventsOn('game', (g) => { game = g; render(); });
  rt.EventsOn('scan-frame', readLiveFrame);
  rt.EventsOn('log-status', (status) => {
    logStatus = status;
    const el = document.getElementById('log-follow-status');
    if (el) el.textContent = `${status.state || 'searching'}: ${status.message || ''}${status.path ? ` (${status.path})` : ''}`;
  });
  rt.EventsOn('mode', setMode);
  rt.EventsOn('solve-now', () => changed(true));
  document.getElementById('to-overlay').onclick = () => api().SetMode('overlay');
  document.getElementById('ov-open').onclick = () => api().SetMode('analyzer');
  document.getElementById('ov-fold').onclick = () => saveSettings({ collapsed: !settings.collapsed });
  document.getElementById('ov-lock').onclick = () => saveSettings({ clickThrough: !settings.clickThrough });
  document.getElementById('reset').onclick = () => { board = blankBoard(); changed(true); };
  document.getElementById('scan-now-full').onclick = scanNow;
  document.getElementById('read-shot').onclick = () => document.getElementById('shot-file').click();
  document.getElementById('shot-file').onchange = (event) => readScreenshot(event.target.files[0]);
  window.addEventListener('paste', (event) => {
    const item = [...(event.clipboardData?.items || [])].find((entry) => entry.type.startsWith('image/'));
    if (item) readScreenshot(item.getAsFile());
  });
  window.addEventListener('dragover', (event) => event.preventDefault());
  window.addEventListener('drop', (event) => {
    event.preventDefault();
    readScreenshot(event.dataTransfer?.files?.[0]);
  });
  document.getElementById('browse-log').onclick = async () => {
    const path = await api().BrowseLog();
    if (path) {
      settings = await api().GetSettings();
      logPathDraft = null;
      logStatus = await api().GetLogStatus();
      render();
    }
  };
  document.getElementById('save-log-path').onclick = async () => {
    settings = await api().SetLogPath(document.getElementById('log-path').value);
    logPathDraft = null;
    logStatus = await api().GetLogStatus();
    render();
  };
  window.addEventListener('mousedown', (event) => {
    if (document.body.classList.contains('overlay') && event.target.closest('.ov-bar') && !event.target.closest('button')) overlayDragging = true;
  });
  window.addEventListener('mouseup', () => {
    if (!document.body.classList.contains('overlay')) return;
    const moved = overlayDragging;
    overlayDragging = false;
    if (moved) api().OverlayMoved();
    resizeOverlayToContent();
  });
  rt.EventsOn('settings', (s) => { settings = s; render(); });
  const restoredMode = await api().RestoreStartupMode();
  setMode(restoredMode);
  render();
  solve();
}

window.addEventListener('DOMContentLoaded', () => {
  const wait = setInterval(() => {
    if (window.go && window.go.main && window.runtime) {
      clearInterval(wait);
      init();
    }
  }, 30);
});
