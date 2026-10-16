import { greeting } from './greeting';
import './index.css';

/** The one element `selector` finds in the page, which index.html must have. */
function find<T extends Element>(selector: string, type: new () => T): T {
  const element = document.querySelector(selector);
  if (!(element instanceof type)) {
    throw new Error(`index.html has no ${selector}`);
  }
  return element;
}

const heading = find('h1', HTMLHeadingElement);
const input = find('#name', HTMLInputElement);
const output = find('p[aria-live]', HTMLParagraphElement);

// The app's name lives in one place: index.html's <title>.
heading.textContent = document.title;
output.textContent = greeting(input.value);
input.addEventListener('input', () => {
  output.textContent = greeting(input.value);
});
