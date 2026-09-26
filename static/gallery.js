(() => {
  const dialog = document.querySelector('#photo-dialog');
  const image = document.querySelector('#dialog-photo');
  const title = document.querySelector('#dialog-title');
  const download = document.querySelector('#dialog-download');
  const form = document.querySelector('#selection-form');
  const status = document.querySelector('#selection-status');
  const button = form.querySelector('.selection-download');
  const limit = 50;

  const cards = [...document.querySelectorAll('.gallery-card')];
  const empty = document.querySelector('#filter-empty');

  document.querySelectorAll('.gallery-open').forEach((link) => {
    link.addEventListener('click', (event) => {
      if (typeof dialog.showModal !== 'function') return;
      event.preventDefault();
      const photoTitle = link.dataset.photoTitle || 'Family photo';
      title.textContent = photoTitle;
      image.alt = photoTitle;
      image.src = link.href;
      download.href = `/media/${encodeURIComponent(link.dataset.photoId)}?download=1`;
      dialog.showModal();
    });
  });

  dialog.addEventListener('close', () => {
    image.removeAttribute('src');
  });

  const updateSelection = () => {
    const checked = form.querySelectorAll('input[name="photo_id"]:checked');
    const count = checked.length;
    const outside = cards.filter((card) => card.hidden && card.querySelector('input[name="photo_id"]').checked).length;
    status.textContent = count
      ? `${count} photo${count === 1 ? '' : 's'} selected (maximum ${limit}); ${outside} selected outside the active filter. Selections remain checked across filters.`
      : `Select photos to download together (up to ${limit}). Selections remain checked across filters.`;
    button.disabled = count === 0;
    form.querySelectorAll('input[name="photo_id"]:not(:checked)').forEach((box) => {
      box.disabled = count >= limit;
    });
  };

  form.addEventListener('change', updateSelection);
  updateSelection();

  document.querySelectorAll('.tag-filter').forEach((filter) => {
    filter.addEventListener('click', () => {
      document.querySelectorAll('.tag-filter').forEach((button) => {
        button.setAttribute('aria-pressed', String(button === filter));
      });
      const tag = filter.dataset.filter;
      let visible = 0;
      cards.forEach((card) => {
        const matches = !tag || JSON.parse(card.dataset.tags).includes(tag);
        card.hidden = !matches;
        if (matches) visible++;
      });
      empty.hidden = visible !== 0;
      updateSelection();
    });
  });
})();
