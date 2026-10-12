import { mount } from 'svelte'
import '@fontsource/golos-text/400.css'
import '@fontsource/golos-text/500.css'
import '@fontsource/golos-text/600.css'
import './app.css'
import App from './App.svelte'
import { i18n } from './lib/i18n/i18n.svelte'

document.documentElement.lang = i18n.lang

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
