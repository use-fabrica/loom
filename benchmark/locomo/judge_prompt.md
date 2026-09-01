# LoCoMo judge prompt (v1)

Binary correctness judge, adapted from LongMemEval's `get_anscheck_prompt`
(`xiaowu0162/LongMemEval`, `src/evaluation/evaluate_qa.py`, pinned at
`9e0b455f4ef0e2ab8f2e582289761153549043fc`). LongMemEval asks the judge a
single yes/no question per QA pair, with a dedicated template for
unanswerable ("abstention") questions; LoCoMo's structural equivalent is its
adversarial category (category 5), so that split is kept here and the
"no information available" framing is carried over from LongMemEval's
abstention prompt to match the official LoCoMo lexical scorer's own check
(`task_eval/evaluation.py:eval_question_answering`, category-5 branch: a
response is correct iff it contains "no information available" or
"not mentioned").

`score.py` fills `{question}` / `{answer}` / `{response}` with `str.format`.

## Standard prompt

I will give you a question, a correct answer, and a response from a model. Please answer yes if the response contains the correct answer. Otherwise, answer no. If the response is equivalent to the correct answer or contains all the intermediate steps to get the correct answer, you should also answer yes. If the response only contains a subset of the information required by the answer, answer no.

Question: {question}

Correct Answer: {answer}

Model Response: {response}

Is the model response correct? Answer yes or no only.

## Adversarial prompt

I will give you an adversarial question that has no correct answer within the conversation, and a response from a model. Please answer yes if the model correctly identifies that the information needed to answer the question is not available (for example, it says the information is not mentioned, not available, or not discussed). Otherwise, answer no.

Question: {question}

Model Response: {response}

Does the model correctly identify the question as unanswerable? Answer yes or no only.
