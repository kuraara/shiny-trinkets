// __NAME__: talks to its Go server's JSON API.
const $ = (s) => document.querySelector(s);
$('#go').addEventListener('click', async () => {
  const who = $('#who').value.trim();
  try {
    const r = await fetch(`api/hello?name=${encodeURIComponent(who)}`);
    const res = await r.json();
    if (!r.ok) throw new Error(res.error || 'Something went wrong');
    $('#out').textContent = res.message;
  } catch (e) {
    $('#out').textContent = e.message;
  }
});
