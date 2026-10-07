import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { App } from './App';
import './index.css';

const root = document.getElementById('root');
if (root === null) {
  throw new Error('index.html has no #root element');
}
// The app's name lives in one place: index.html's <title>.
createRoot(root).render(
  <StrictMode>
    <App title={document.title} />
  </StrictMode>,
);
