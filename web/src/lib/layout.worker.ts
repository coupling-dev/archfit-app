// The graph's layout worker: elkjs's own worker script, which answers elk-api's messages from inside a worker. Vite
// builds it as a hashed file of its own, served from 'self', so the CSP needs only worker-src 'self'.
import 'elkjs/lib/elk-worker.min.js';
