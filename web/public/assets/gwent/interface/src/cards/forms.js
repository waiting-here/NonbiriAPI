// Generated forms have stable IDs but are never selectable deck entries.
export function runtimeForms(catalog) {
  return catalog.flatMap((source) =>
    ['transformForm', 'avengerForm'].flatMap((key) => {
      const form = source[key];
      if (!form) return [];
      return [
        {
          ...source,
          transformForm: undefined,
          avengerForm: undefined,
          ...form,
          type: 'unit',
          maxCopies: 1,
          starterCopies: 0,
          generated: true,
          ephemeral: key === 'avengerForm',
          originId: source.id,
          category: 'generated',
        },
      ];
    }),
  );
}
