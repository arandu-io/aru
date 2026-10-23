package main

// recorderScript runs in every document the recorder opens, before the
// document's own scripts. It reports what the person does through
// console.debug, which the driver hands back as console events: the address
// each document loads at, and each click, fill, choice, tick, submission and
// Escape, with the element named the way the test kit names it -- role and
// accessible name, label, test id, visible text, and a CSS path only when none
// of those exists.
//
// A field is reported on change, which fires when it loses focus: before the
// click on the button that submits it. A click on a field, a label or a
// checkbox is left to the change it causes. A submission that follows a
// recorded click on its own submit button is that click, and is not reported
// twice.
const recorderScript = `(() => {
  if (window.__aranduRecorder || location.protocol === 'about:') return;
  window.__aranduRecorder = true;
  const send = m => console.debug('` + recordSignal + `', JSON.stringify(m));
  send({kind: 'load', url: location.href});

  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  const implicitRole = el => {
    const t = el.tagName.toLowerCase();
    const type = (el.getAttribute('type') || '').toLowerCase();
    switch (t) {
      case 'a': return el.hasAttribute('href') ? 'link' : null;
      case 'button': case 'summary': return 'button';
      case 'input':
        if (['button', 'submit', 'reset', 'image'].includes(type)) return 'button';
        if (type === 'checkbox') return 'checkbox';
        if (type === 'radio') return 'radio';
        if (['', 'text', 'email', 'tel', 'url', 'password', 'search', 'number'].includes(type)) return 'textbox';
        return null;
      case 'select': return 'combobox';
      case 'textarea': return 'textbox';
      case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6': return 'heading';
    }
    return null;
  };
  const roleOf = el => { const r = el.getAttribute('role'); return r ? r.trim().split(/\s+/)[0] : implicitRole(el); };
  const byIds = ids => ids.split(/\s+/).map(id => document.getElementById(id)).filter(Boolean).map(e => e.textContent).join(' ');
  const labelOf = el => {
    const lb = el.getAttribute('aria-labelledby');
    if (lb) { const s = norm(byIds(lb)); if (s) return s; }
    const al = norm(el.getAttribute('aria-label'));
    if (al) return al;
    if (el.labels && el.labels.length) {
      return norm(Array.from(el.labels).map(l => {
        const c = l.cloneNode(true);
        c.querySelectorAll('input,select,textarea').forEach(x => x.remove());
        return c.textContent;
      }).join(' '));
    }
    return '';
  };
  const nameOf = el => {
    const label = labelOf(el);
    if (label) return label;
    const t = el.tagName.toLowerCase();
    if (t === 'input' && ['button', 'submit', 'reset'].includes((el.type || '').toLowerCase())) return norm(el.value);
    if (t === 'input' || t === 'textarea') return norm(el.getAttribute('placeholder'));
    if (t === 'img') return norm(el.getAttribute('alt'));
    return norm(el.innerText || el.textContent) || norm(el.getAttribute('title'));
  };
  const cssPath = el => {
    const parts = [];
    for (let e = el; e && e.nodeType === 1 && e !== document.body; e = e.parentElement) {
      if (e.id) { parts.unshift('#' + CSS.escape(e.id)); break; }
      let i = 1;
      for (let s = e.previousElementSibling; s; s = s.previousElementSibling) if (s.tagName === e.tagName) i++;
      parts.unshift(e.tagName.toLowerCase() + ':nth-of-type(' + i + ')');
    }
    return parts.join(' > ');
  };
  const named = ['button', 'link', 'tab', 'menuitem', 'option', 'switch', 'heading', 'checkbox', 'radio', 'textbox', 'combobox'];
  const locate = el => {
    const role = roleOf(el);
    const name = nameOf(el);
    if (role && named.includes(role) && name && !['textbox', 'combobox', 'checkbox', 'radio'].includes(role)) return {kind: 'role', a: role, b: name};
    const id = el.getAttribute('data-testid');
    if (id) return {kind: 'testid', a: id};
    const text = norm(el.innerText || el.textContent);
    if (text && text.length <= 80) return {kind: 'text', a: text};
    return {kind: 'css', a: cssPath(el)};
  };
  const locateControl = el => {
    const label = labelOf(el);
    if (label) return {kind: 'label', a: label};
    const role = roleOf(el);
    const name = nameOf(el);
    if (role && name) return {kind: 'role', a: role, b: name};
    const id = el.getAttribute('data-testid');
    if (id) return {kind: 'testid', a: id};
    return {kind: 'css', a: cssPath(el)};
  };
  const isField = el => {
    const t = el.tagName.toLowerCase();
    if (t === 'select' || t === 'textarea' || el.isContentEditable) return true;
    if (t !== 'input') return false;
    return !['button', 'submit', 'reset', 'image'].includes((el.type || '').toLowerCase());
  };

  let lastSubmitter = null, lastClickAt = 0;
  document.addEventListener('click', e => {
    if (!e.isTrusted) return;
    const el = e.target.closest('a[href],button,summary,input,select,textarea,label,[role],[data-testid]') || e.target;
    if (isField(el) || (el.tagName === 'LABEL' && el.control)) return;
    lastSubmitter = (el.type === 'submit' || el.type === 'image') ? el : null;
    lastClickAt = Date.now();
    send({kind: 'click', loc: locate(el)});
  }, true);
  document.addEventListener('change', e => {
    const el = e.target;
    if (!(el instanceof Element) || !isField(el)) return;
    const type = (el.type || '').toLowerCase();
    if (type === 'checkbox' || type === 'radio') {
      send({kind: el.checked ? 'check' : 'uncheck', loc: locateControl(el)});
    } else if (el.tagName === 'SELECT') {
      const o = el.options[el.selectedIndex];
      send({kind: 'select', loc: locateControl(el), value: o ? norm(o.text) : ''});
    } else {
      send({kind: 'fill', loc: locateControl(el), value: el.isContentEditable ? el.innerText : el.value, secret: type === 'password'});
    }
  }, true);
  document.addEventListener('submit', e => {
    if (e.submitter && e.submitter === lastSubmitter && Date.now() - lastClickAt < 2000) return;
    send({kind: 'submit'});
  }, true);
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') send({kind: 'press', value: 'Escape'});
  }, true);
})();`
