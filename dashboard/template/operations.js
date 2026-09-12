document.addEventListener('htmx:beforeSwap', function (event) {
  if (event.detail.xhr.getResponseHeader('HX-Retarget') === '#review-feedback') {
    event.detail.shouldSwap = true;
    event.detail.isError = false;
  }
});
document.addEventListener('htmx:sendError', function () {
  const feedback = document.getElementById('review-feedback');
  if (feedback) feedback.textContent = 'Connection interrupted. Retry the same form to check whether the action completed.';
});
document.addEventListener('htmx:responseError', function (event) {
  const feedback = document.getElementById('review-feedback');
  if (feedback) feedback.textContent = event.detail.xhr.status === 401 || event.detail.xhr.status === 403
    ? 'Your session or permissions no longer allow this action. Reload the page before continuing.'
    : 'The request could not be completed. Reload to check the latest status.';
});
