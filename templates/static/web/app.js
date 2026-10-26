// __NAME__: runs entirely in the browser.
const $ = (s) => document.querySelector(s);
$('#go').addEventListener('click', () => {
  const who = $('#who').value.trim() || 'there';
  $('#out').textContent = `Hello, ${who}!`;
});
