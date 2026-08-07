# Realce de sintaxe para `.kyse.go`

Um `.kyse.go` não é Go. Sem dizer isso ao editor, o `gopls` tenta analisá-lo,
marca erro em toda diretiva, e o arquivo fica vermelho estando certo.

## O que tem aqui

| Arquivo | O que é |
|---|---|
| `settings.json` | copiado para o `.vscode/` do projeto pelo `aru new`. Resolve o principal: associa `.kyse.go` à linguagem `kyse` e desliga o format-on-save |
| `kyse.tmLanguage.json` | a gramática TextMate: diretivas, `{{ }}`, `{!! !!}`, e o bloco `@go` realçado como Go |
| `language-configuration.json` | indentação automática — `@section` indenta, `@endsection` desindenta |

## O que **não** tem, e por quê

Falta o `package.json`, que é o manifesto de uma extensão VS Code. Sem ele estes
arquivos não viram uma extensão instalável.

Isso não é esquecimento. A **REGRA 13** proíbe `package.json` em qualquer
repositório do projeto, e o `plans/checklist.sh` verifica isso por comando. A
regra existe para uma razão precisa — nenhum projeto Arandu precisa de Node para
compilar ou rodar — e uma extensão de editor não muda isso.

Mas a regra está escrita sem exceção, e abrir uma em silêncio é como o Node
entrou no Laravel: pela página de erro, por um caso pontual que parecia
inofensivo.

**Então fica em aberto, para o proprietário decidir.** Três caminhos:

1. **Repositório próprio** `arandu-io/vscode-kyse`. É o décimo primeiro, e a
   REGRA 3 exige pedir. Isolamento total: o `package.json` não encosta no `aru`
2. **Exceção escrita na REGRA 13** para `aru/editors/`, com ADR registrando que
   ferramenta de editor não é dependência de build
3. **Ficar como está.** O `settings.json` já entrega o essencial: o editor para
   de acusar erro falso. O realce fino fica sem publicação

## Enquanto isso, instalando à mão

```bash
mkdir -p ~/.vscode/extensions/kyse
cp kyse.tmLanguage.json language-configuration.json ~/.vscode/extensions/kyse/
# e escrever o package.json localmente, fora de qualquer repositório do projeto
```
