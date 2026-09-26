const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

test('selected photos persist across filters and status counts hidden selections', () => {
  const handlers = new Map();
  const makeBox = () => ({ checked: false, disabled: false });
  const first = makeBox();
  const second = makeBox();
  const cards = [
    { hidden: false, dataset: { tags: '["summer"]' }, querySelector: () => first },
    { hidden: false, dataset: { tags: '["winter"]' }, querySelector: () => second },
  ];
  const filters = ['', 'summer', 'winter'].map((tag) => ({
    dataset: { filter: tag },
    addEventListener: (event, callback) => handlers.set(tag, callback),
    setAttribute() {},
  }));
  const status = { textContent: '' };
  const button = { disabled: false };
  const form = {
    querySelector: () => button,
    querySelectorAll: (selector) => selector.endsWith(':checked')
      ? [first, second].filter((box) => box.checked)
      : [first, second].filter((box) => !box.checked),
    addEventListener: (event, callback) => handlers.set(event, callback),
  };
  const dialog = { addEventListener() {} };
  const empty = { hidden: true };
  const elements = {
    '#photo-dialog': dialog, '#dialog-photo': {}, '#dialog-title': {},
    '#dialog-download': {}, '#selection-form': form, '#selection-status': status,
    '#filter-empty': empty,
  };
  const document = {
    querySelector: (selector) => elements[selector],
    querySelectorAll: (selector) => ({ '.gallery-card': cards, '.tag-filter': filters, '.gallery-open': [] })[selector],
  };
  const source = fs.readFileSync(path.join(__dirname, '../static/gallery.js'), 'utf8');
  vm.runInNewContext(source, { document });

  first.checked = true;
  handlers.get('change')();
  assert.match(status.textContent, /1 photo selected.*0 selected outside the active filter/);
  handlers.get('winter')();
  assert.equal(first.checked, true);
  assert.equal(cards[0].hidden, true);
  assert.match(status.textContent, /1 photo selected.*1 selected outside the active filter/);

  second.checked = true;
  handlers.get('change')();
  assert.match(status.textContent, /2 photos selected.*1 selected outside the active filter/);
  handlers.get('summer')();
  assert.equal(second.checked, true);
  assert.match(status.textContent, /2 photos selected.*1 selected outside the active filter/);
  handlers.get('')();
  assert.match(status.textContent, /2 photos selected.*0 selected outside the active filter/);
  assert.equal(button.disabled, false);
});
