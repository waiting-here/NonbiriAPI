export const rejectionDescriptionKeys: Record<string, string> = {
  'query parameters are not supported':
    'logs.rejectionDescriptions.query parameters are not supported',
  'expected application/json with optional UTF-8 charset':
    'logs.rejectionDescriptions.expected application/json with optional UTF-8 charset',
  'content encoding is not supported':
    'logs.rejectionDescriptions.content encoding is not supported',
  'request body could not be read': 'logs.rejectionDescriptions.request body could not be read',
  'request body exceeds the configured limit':
    'logs.rejectionDescriptions.request body exceeds the configured limit',
  'expected one valid UTF-8 JSON object':
    'logs.rejectionDescriptions.expected one valid UTF-8 JSON object',
  'too many top-level fields': 'logs.rejectionDescriptions.too many top-level fields',
  'invalid top-level field name': 'logs.rejectionDescriptions.invalid top-level field name',
  'duplicate top-level field': 'logs.rejectionDescriptions.duplicate top-level field',
  'trailing JSON values are not allowed':
    'logs.rejectionDescriptions.trailing JSON values are not allowed',
  'required field is missing': 'logs.rejectionDescriptions.required field is missing',
  'expected a nonempty model name of at most 133 characters without control characters':
    'logs.rejectionDescriptions.expected a nonempty model name of at most 133 characters without control characters',
  'expected a boolean or null': 'logs.rejectionDescriptions.expected a boolean or null',
  'expected false; embeddings do not support streaming':
    'logs.rejectionDescriptions.expected false; embeddings do not support streaming',
  'expected a nonempty string, token array, or batch of at most 2048 inputs':
    'logs.rejectionDescriptions.expected a nonempty string, token array, or batch of at most 2048 inputs',
  'expected float or base64': 'logs.rejectionDescriptions.expected float or base64',
  'expected a positive integer up to 2147483647':
    'logs.rejectionDescriptions.expected a positive integer up to 2147483647',
  'expected a string of at most 512 characters without control characters':
    'logs.rejectionDescriptions.expected a string of at most 512 characters without control characters',
  'expected an object or null': 'logs.rejectionDescriptions.expected an object or null',
  'GET model discovery does not accept a request body':
    'logs.rejectionDescriptions.GET model discovery does not accept a request body',
  'model identity is invalid or ambiguous':
    'logs.rejectionDescriptions.model identity is invalid or ambiguous',
  'model was not found or is unavailable':
    'logs.rejectionDescriptions.model was not found or is unavailable',
  'account level does not allow this model':
    'logs.rejectionDescriptions.account level does not allow this model',
  'unsupported operation': 'logs.rejectionDescriptions.unsupported operation',
  'unsupported request feature or unknown field':
    'logs.rejectionDescriptions.unsupported request feature or unknown field',
  'request cannot be represented by this connector':
    'logs.rejectionDescriptions.request cannot be represented by this connector',
  'output budget exceeds the configured model limit':
    'logs.rejectionDescriptions.output budget exceeds the configured model limit',
  'unsupported effort value': 'logs.rejectionDescriptions.unsupported effort value',
  'effort is not enabled for this model':
    'logs.rejectionDescriptions.effort is not enabled for this model',
  'expected a boolean': 'logs.rejectionDescriptions.expected a boolean',
  'only false is allowed by the configured omission policy':
    'logs.rejectionDescriptions.only false is allowed by the configured omission policy',
  'storage control is not verified for this model':
    'logs.rejectionDescriptions.storage control is not verified for this model',
  'unsupported top-level field': 'logs.rejectionDescriptions.unsupported top-level field',
  'expected an integer from 1 to 2147483647':
    'logs.rejectionDescriptions.expected an integer from 1 to 2147483647',
  'conflicting output budgets': 'logs.rejectionDescriptions.conflicting output budgets',
  'native extension conflicts with translated controls':
    'logs.rejectionDescriptions.native extension conflicts with translated controls',
  'configured provider cannot require a tool call':
    'logs.rejectionDescriptions.configured provider cannot require a tool call',
  'configured provider requires a value from 0 to 1':
    'logs.rejectionDescriptions.configured provider requires a value from 0 to 1',
  'configured provider omits top_p when temperature is present':
    'logs.rejectionDescriptions.configured provider omits top_p when temperature is present',
  'configured provider would omit this field':
    'logs.rejectionDescriptions.configured provider would omit this field',
  'expected text or text blocks': 'logs.rejectionDescriptions.expected text or text blocks',
  'only text blocks are supported': 'logs.rejectionDescriptions.only text blocks are supported',
  'an intermediate tool-result cache position cannot be represented':
    'logs.rejectionDescriptions.an intermediate tool-result cache position cannot be represented',
  'OpenAI key storage policy is incompatible with this connector':
    'logs.rejectionDescriptions.OpenAI key storage policy is incompatible with this connector',
  'stream options cannot be converted':
    'logs.rejectionDescriptions.stream options cannot be converted',
  'role cannot be passed through to this connector':
    'logs.rejectionDescriptions.role cannot be passed through to this connector',
  'messages could not be restored from flattened tool calls':
    'logs.rejectionDescriptions.messages could not be restored from flattened tool calls',
  'messages do not match the configured role policy':
    'logs.rejectionDescriptions.messages do not match the configured role policy',
  'no connector supports the required request features':
    'logs.rejectionDescriptions.no connector supports the required request features',
  'request adaptation contains an unsupported field or value':
    'logs.rejectionDescriptions.request adaptation contains an unsupported field or value',
  'expected an ephemeral cache marker':
    'logs.rejectionDescriptions.expected an ephemeral cache marker',
  'cache lifetime must be 5m or 1h': 'logs.rejectionDescriptions.cache lifetime must be 5m or 1h',
  'cache mapping is not enabled for this model':
    'logs.rejectionDescriptions.cache mapping is not enabled for this model',
  'duplicate cache controls': 'logs.rejectionDescriptions.duplicate cache controls',
  'no eligible cache block': 'logs.rejectionDescriptions.no eligible cache block',
  'final cache block has a conflicting lifetime':
    'logs.rejectionDescriptions.final cache block has a conflicting lifetime',
  'at most four cache breakpoints are allowed':
    'logs.rejectionDescriptions.at most four cache breakpoints are allowed',
  '1h cache breakpoints must precede 5m cache breakpoints':
    'logs.rejectionDescriptions.1h cache breakpoints must precede 5m cache breakpoints',
  'expected an array of message objects':
    'logs.rejectionDescriptions.expected an array of message objects',
  'expected a message object': 'logs.rejectionDescriptions.expected a message object',
  'content block type and text must be strings':
    'logs.rejectionDescriptions.content block type and text must be strings',
};
