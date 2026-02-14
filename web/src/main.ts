import { createElement } from 'react';
import ReactDOM from 'react-dom/client';

import './style.css';

import { App } from './App';

const root = document.getElementById('root');
if (!root) {
  throw new Error('missing host DOM');
}

ReactDOM.createRoot(root).render(createElement(App));
