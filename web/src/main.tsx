import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import './style.css';
import 'xterm/css/xterm.css';

const root = document.getElementById('root');
if (!root) throw new Error('missing #root element');

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
