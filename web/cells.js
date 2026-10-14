// The web font has only letters: borders and symbols come from whatever
// font the device has, which on a phone is rarely as wide. Each of them
// gets a cell of its own, so that the columns of the drawings stay in line.
(() => {
  const wide = /[^\x00-\x7f]/;
  for (const pre of document.querySelectorAll(".abilities pre")) {
    const walker = document.createTreeWalker(pre, NodeFilter.SHOW_TEXT);
    const texts = [];
    while (walker.nextNode()) texts.push(walker.currentNode);
    for (const text of texts) {
      if (!wide.test(text.data)) continue;
      const cells = document.createDocumentFragment();
      for (const c of text.data) {
        const cell = wide.test(c) ? document.createElement("i") : document.createTextNode(c);
        cell.textContent = c;
        cells.append(cell);
      }
      text.replaceWith(cells);
    }
  }
})();
