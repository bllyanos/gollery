document.querySelectorAll("form[data-confirm]").forEach((form) => {
  form.addEventListener("submit", (event) => {
    if (!window.confirm(form.dataset.confirm)) event.preventDefault();
  });
});

const selection = document.querySelector("#delete-selected");
if (selection) {
  const boxes = [...document.querySelectorAll('input[form="delete-selected"]')];
  const count = selection.querySelector("[data-selected-count]");
  const button = selection.querySelector("button");
  const refresh = () => {
    const selected = boxes.filter((box) => box.checked).length;
    count.textContent = `${selected} selected (max 50)`;
    button.disabled = selected === 0 || selected > 50;
  };
  boxes.forEach((box) => box.addEventListener("change", refresh));
  refresh();
}
