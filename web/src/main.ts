import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { applyTheme, storedTheme } from './lib/theme';

applyTheme(storedTheme());
mount(App, { target: document.getElementById('app')! });
