declare module 'virtual:station-core/*' {
  const resource: import('i18next').ResourceLanguage;
  export default resource;
}

declare module 'virtual:feature-copy/*' {
  const url: string;
  export default url;
}
