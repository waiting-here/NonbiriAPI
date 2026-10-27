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
  'expected a nonempty string': 'logs.rejectionDescriptions.expected a nonempty string',
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
  'Gateway image generation does not support streaming':
    'logs.rejectionDescriptions.Gateway image generation does not support streaming',
  'Gateway images require b64_json': 'logs.rejectionDescriptions.Gateway images require b64_json',
  'Responses fields cannot be represented by the selected Chat protocol':
    'logs.rejectionDescriptions.Responses fields cannot be represented by the selected Chat protocol',
  'Responses input cannot be represented as complete Chat messages':
    'logs.rejectionDescriptions.Responses input cannot be represented as complete Chat messages',
  'System One option headers require a single true value; images and box questions require the images opt-in':
    'logs.rejectionDescriptions.System One option headers require a single true value; images and box questions require the images opt-in',
  'a nonempty prompt is required': 'logs.rejectionDescriptions.a nonempty prompt is required',
  'a query and 1 to 1000 documents are required':
    'logs.rejectionDescriptions.a query and 1 to 1000 documents are required',
  'a query is required': 'logs.rejectionDescriptions.a query is required',
  'a required inference field cannot be excluded':
    'logs.rejectionDescriptions.a required inference field cannot be excluded',
  'a text prompt is required': 'logs.rejectionDescriptions.a text prompt is required',
  'an uploaded audio file is required':
    'logs.rejectionDescriptions.an uploaded audio file is required',
  'decisions do not support streaming':
    'logs.rejectionDescriptions.decisions do not support streaming',
  'dimensions require Gateway v4': 'logs.rejectionDescriptions.dimensions require Gateway v4',
  'duplicate multipart field': 'logs.rejectionDescriptions.duplicate multipart field',
  'expected 1 to 1000 documents': 'logs.rejectionDescriptions.expected 1 to 1000 documents',
  'expected 1 to 400 predicate, choice, or score questions within request limits':
    'logs.rejectionDescriptions.expected 1 to 400 predicate, choice, or score questions within request limits',
  'expected a Gateway embedding request':
    'logs.rejectionDescriptions.expected a Gateway embedding request',
  'expected a Gateway endpoint': 'logs.rejectionDescriptions.expected a Gateway endpoint',
  'expected a count no larger than documents':
    'logs.rejectionDescriptions.expected a count no larger than documents',
  'expected a nonempty message array with matching tool calls':
    'logs.rejectionDescriptions.expected a nonempty message array with matching tool calls',
  'expected a nonempty query': 'logs.rejectionDescriptions.expected a nonempty query',
  'expected a number': 'logs.rejectionDescriptions.expected a number',
  'expected a number within the supported sampling range':
    'logs.rejectionDescriptions.expected a number within the supported sampling range',
  'expected a positive count no larger than documents':
    'logs.rejectionDescriptions.expected a positive count no larger than documents',
  'expected a safely representable integer':
    'logs.rejectionDescriptions.expected a safely representable integer',
  'expected a string of at most 128 characters or null':
    'logs.rejectionDescriptions.expected a string of at most 128 characters or null',
  'expected a string of at most 64 characters or null':
    'logs.rejectionDescriptions.expected a string of at most 64 characters or null',
  'expected a string without control characters':
    'logs.rejectionDescriptions.expected a string without control characters',
  'expected a supported tool choice': 'logs.rejectionDescriptions.expected a supported tool choice',
  'expected a valid model name': 'logs.rejectionDescriptions.expected a valid model name',
  'expected an array of supported function tools':
    'logs.rejectionDescriptions.expected an array of supported function tools',
  'expected an image count from 1 to 10':
    'logs.rejectionDescriptions.expected an image count from 1 to 10',
  'expected an image generation request':
    'logs.rejectionDescriptions.expected an image generation request',
  'expected an integer from 1 to 128':
    'logs.rejectionDescriptions.expected an integer from 1 to 128',
  'expected an integer in the supported range':
    'logs.rejectionDescriptions.expected an integer in the supported range',
  'expected audio or sse': 'logs.rejectionDescriptions.expected audio or sse',
  'expected false for this connector':
    'logs.rejectionDescriptions.expected false for this connector',
  'expected inline text or input items':
    'logs.rejectionDescriptions.expected inline text or input items',
  'expected multipart/form-data with a boundary':
    'logs.rejectionDescriptions.expected multipart/form-data with a boundary',
  'expected one JSON object': 'logs.rejectionDescriptions.expected one JSON object',
  'expected only include_usage with a boolean value':
    'logs.rejectionDescriptions.expected only include_usage with a boolean value',
  'expected strings or document objects':
    'logs.rejectionDescriptions.expected strings or document objects',
  'expected supported messages or matching tool history with at most 128 images':
    'logs.rejectionDescriptions.expected supported messages or matching tool history with at most 128 images',
  'expected supported stop sequences':
    'logs.rejectionDescriptions.expected supported stop sequences',
  'expected text, tokens, or a nonempty batch':
    'logs.rejectionDescriptions.expected text, tokens, or a nonempty batch',
  'expected the integer 1': 'logs.rejectionDescriptions.expected the integer 1',
  'expected two positive integers separated by x':
    'logs.rejectionDescriptions.expected two positive integers separated by x',
  'expected two positive integers separated by :':
    'logs.rejectionDescriptions.expected two positive integers separated by :',
  'field cannot be represented by this connector':
    'logs.rejectionDescriptions.field cannot be represented by this connector',
  'inline image URLs or uploaded image parts are required':
    'logs.rejectionDescriptions.inline image URLs or uploaded image parts are required',
  'inline input is required': 'logs.rejectionDescriptions.inline input is required',
  'invalid System One request field': 'logs.rejectionDescriptions.invalid System One request field',
  'invalid multipart body': 'logs.rejectionDescriptions.invalid multipart body',
  'invalid multipart field': 'logs.rejectionDescriptions.invalid multipart field',
  'invalid number of uploaded files': 'logs.rejectionDescriptions.invalid number of uploaded files',
  'invalid upload': 'logs.rejectionDescriptions.invalid upload',
  'invalid upload content type': 'logs.rejectionDescriptions.invalid upload content type',
  'mixed text and object documents cannot be represented':
    'logs.rejectionDescriptions.mixed text and object documents cannot be represented',
  'model does not support this request type':
    'logs.rejectionDescriptions.model does not support this request type',
  'moderations do not support streaming':
    'logs.rejectionDescriptions.moderations do not support streaming',
  'multipart text fields must be UTF-8':
    'logs.rejectionDescriptions.multipart text fields must be UTF-8',
  'native extension conflicts with translated input':
    'logs.rejectionDescriptions.native extension conflicts with translated input',
  'only rerank is supported by this entry point':
    'logs.rejectionDescriptions.only rerank is supported by this entry point',
  'only text embedding inputs are supported':
    'logs.rejectionDescriptions.only text embedding inputs are supported',
  'persistent and background requests are not supported':
    'logs.rejectionDescriptions.persistent and background requests are not supported',
  'persistent resource references are not supported':
    'logs.rejectionDescriptions.persistent resource references are not supported',
  'request validation failed without a recorded field':
    'logs.rejectionDescriptions.request validation failed without a recorded field',
  'rerank does not support streaming':
    'logs.rejectionDescriptions.rerank does not support streaming',
  'rerank field is not supported by the selected protocol':
    'logs.rejectionDescriptions.rerank field is not supported by the selected protocol',
  'stored prompt references are not supported':
    'logs.rejectionDescriptions.stored prompt references are not supported',
  'text input and a built-in voice name are required':
    'logs.rejectionDescriptions.text input and a built-in voice name are required',
  'text or image input is required': 'logs.rejectionDescriptions.text or image input is required',
  'the first multipart part must contain the JSON request':
    'logs.rejectionDescriptions.the first multipart part must contain the JSON request',
  'the selected protocol requires text documents':
    'logs.rejectionDescriptions.the selected protocol requires text documents',
  'this operation does not support streaming':
    'logs.rejectionDescriptions.this operation does not support streaming',
  'too many completion results': 'logs.rejectionDescriptions.too many completion results',
  'tool flattening is unavailable for Responses conversion':
    'logs.rejectionDescriptions.tool flattening is unavailable for Responses conversion',
  'tools requiring hosted resources are not supported':
    'logs.rejectionDescriptions.tools requiring hosted resources are not supported',
  'translations do not support streaming':
    'logs.rejectionDescriptions.translations do not support streaming',
  'unsupported Gateway image field': 'logs.rejectionDescriptions.unsupported Gateway image field',
  'unsupported Gateway rerank field': 'logs.rejectionDescriptions.unsupported Gateway rerank field',
  'unsupported audio upload field': 'logs.rejectionDescriptions.unsupported audio upload field',
  'unsupported cache options': 'logs.rejectionDescriptions.unsupported cache options',
  'unsupported decisions field': 'logs.rejectionDescriptions.unsupported decisions field',
  'unsupported embedding field': 'logs.rejectionDescriptions.unsupported embedding field',
  'unsupported image upload field': 'logs.rejectionDescriptions.unsupported image upload field',
  'unsupported message role or content':
    'logs.rejectionDescriptions.unsupported message role or content',
  'use only one result count field': 'logs.rejectionDescriptions.use only one result count field',
  'variations do not support streaming':
    'logs.rejectionDescriptions.variations do not support streaming',
  'expected valid and consistent output-token limits':
    'logs.rejectionDescriptions.expected valid and consistent output-token limits',
  'Remove URL query parameters and send supported fields in the JSON body.':
    'logs.rejectionSuggestions.Remove URL query parameters and send supported fields in the JSON body.',
  'Send an uncompressed UTF-8 JSON body with Content-Type: application/json.':
    'logs.rejectionSuggestions.Send an uncompressed UTF-8 JSON body with Content-Type: application/json.',
  'Send the complete request body and retry the request.':
    'logs.rejectionSuggestions.Send the complete request body and retry the request.',
  'Send one valid JSON object with each top-level field appearing once.':
    'logs.rejectionSuggestions.Send one valid JSON object with each top-level field appearing once.',
  'Use one output-token limit, or give both limit fields the same value.':
    'logs.rejectionSuggestions.Use one output-token limit, or give both limit fields the same value.',
  'Choose an available model from the model list for this account.':
    'logs.rejectionSuggestions.Choose an available model from the model list for this account.',
  'Add the required field using the format expected by this API.':
    'logs.rejectionSuggestions.Add the required field using the format expected by this API.',
  'Add more message text to meet the minimum content length.':
    'logs.rejectionSuggestions.Add more message text to meet the minimum content length.',
  'Remove or adjust the unsupported option, or choose a model that supports it.':
    'logs.rejectionSuggestions.Remove or adjust the unsupported option, or choose a model that supports it.',
  'Check message roles, content blocks and matching tool-call IDs.':
    'logs.rejectionSuggestions.Check message roles, content blocks and matching tool-call IDs.',
  'Check function definitions and select a tool declared in the request.':
    'logs.rejectionSuggestions.Check function definitions and select a tool declared in the request.',
  'Set a positive integer within the model limit and keep output-token limits consistent.':
    'logs.rejectionSuggestions.Set a positive integer within the model limit and keep output-token limits consistent.',
  'Use a supported streaming value and valid stream options.':
    'logs.rejectionSuggestions.Use a supported streaming value and valid stream options.',
  'Correct the field type or value according to the stated requirement.':
    'logs.rejectionSuggestions.Correct the field type or value according to the stated requirement.',
  'no usable model resource is available for this request':
    'logs.rejectionDescriptions.no usable model resource is available for this request',
  'shared model resources are temporarily at capacity':
    'logs.rejectionDescriptions.shared model resources are temporarily at capacity',
  'No upstream request was sent. Retry later or choose another available model.':
    'logs.rejectionSuggestions.No upstream request was sent. Retry later or choose another available model.',
  'The specific cause was not recorded. Share the request ID with support if the problem persists.':
    'logs.rejectionSuggestions.The specific cause was not recorded. Share the request ID with support if the problem persists.',
};
