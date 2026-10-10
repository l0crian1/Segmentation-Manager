import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Local development only. Production assets are served directly by the Go HTTPS server.
export default defineConfig({plugins:[react()],server:{host:'127.0.0.1',proxy:{'/api':{target:'https://127.0.0.1:8443',secure:false,changeOrigin:true,configure(proxy){proxy.on('proxyReq',req=>req.setHeader('Origin','https://127.0.0.1:8443'));}}}}});
