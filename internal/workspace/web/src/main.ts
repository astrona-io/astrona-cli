// boot must stay the first import: it takes the launch code out of the
// address bar synchronously, before any other module runs or awaits.
import './boot'
import { createApp } from 'vue'
import './styles/fonts.css'
import './styles/tokens.css'
import './styles/base.css'
import './styles/workspace.css'
import App from './App.vue'

createApp(App).mount('#app')
