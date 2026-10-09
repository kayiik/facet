import { inspectMedia } from './uat-media.mjs';
try {
  console.log(JSON.stringify(inspectMedia(process.argv[2] || 'renders/final.mp4'), null, 2));
} catch (error) {
  console.error(`Artifact verification failed: ${error.message}`);
  process.exitCode = 1;
}
