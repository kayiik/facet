// A Copy button on every command.
const status = document.createElement('p');
status.className = 'sr-only';
status.setAttribute('role', 'status');
document.body.append(status);

for (const block of document.querySelectorAll('pre')) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'copy';
  button.textContent = 'Copy';
  button.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(block.querySelector('code').textContent);
      button.textContent = 'Copied';
      status.textContent = 'Copied the command.';
    } catch {
      button.textContent = 'Select it';
      status.textContent = 'Copying is blocked here; select the command and copy it.';
    }
    setTimeout(() => { button.textContent = 'Copy'; status.textContent = ''; }, 2000);
  });
  block.append(button);
}
