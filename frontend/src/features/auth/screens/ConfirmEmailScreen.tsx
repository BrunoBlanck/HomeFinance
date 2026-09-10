import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { type ReactNode, useRef, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { ApiError } from '@/api/client'
import { Alert } from '@/components/Alert/Alert'
import { FlashAlert } from '@/components/Alert/FlashAlert'
import { Button } from '@/components/Button/Button'
import { CodeInput } from '@/components/CodeInput/CodeInput'
import { TextLink } from '@/components/TextLink/TextLink'
import { messageForError } from '@/lib/errors'
import { sessionQueryOptions } from '@/lib/session'
import { resendCode, verifyEmail } from '../api/auth'
import { AuthForm } from '../components/AuthForm'
import { AuthLayout } from '../components/AuthLayout'
import { ResendCode } from '../components/ResendCode'
import { clearResendCooldown, useResendCooldown } from '../hooks/useResendCooldown'
import { verifyEmailSchema } from '../schemas/auth'
import {
  clearRegistration,
  readRegistrationTokenFor,
  saveRegistration,
} from '../storage/registrationToken'

/** Uma única redação para código errado, expirado, já usado, tentativas
 *  esgotadas e conta inexistente. Qualquer variação viraria um oráculo de
 *  quais e-mails existem — e "restam N tentativas" seria o pior deles. */
const INVALID_CODE = 'Código inválido ou expirado. Peça um novo código se precisar.'

export function ConfirmEmailScreen() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const stateEmail = useRouterState({ select: (state) => state.location.state.email })
  const flash = useRouterState({ select: (state) => state.location.state.flash })
  const codeRef = useRef<HTMLInputElement>(null)
  const [resendDone, setResendDone] = useState(false)
  const { secondsLeft, startNextCooldown } = useResendCooldown()

  // O endereço vem SEMPRE do state do roteador — que sobrevive ao F5, ao
  // contrário do que a spec original supunha (verificado no navegador). Não há
  // campo de e-mail nesta tela de propósito: uma aba que não sabe qual endereço
  // pediu o código também não sabe se o token que ela guarda governa o endereço
  // que a pessoa digitaria, e essa confusão é justamente o que abre brecha.
  const knownEmail = stateEmail ?? ''

  // O token só conta se for o DESTE endereço. Token de outra tentativa vale o
  // mesmo que token nenhum, porque é assim que o servidor o trata: a busca da
  // tentativa é pelo par (e-mail, hash do token).
  const [token, setToken] = useState(() => readRegistrationTokenFor(knownEmail))
  const hasToken = token !== null

  const form = useForm({
    resolver: zodResolver(verifyEmailSchema),
    defaultValues: { email: knownEmail, code: '' },
    mode: 'onSubmit',
    reValidateMode: 'onBlur',
    shouldFocusError: true,
  })

  const verify = useMutation({
    mutationFn: verifyEmail,
    onSuccess(session) {
      // Capacidade de uso único: cumprida a verificação, o par não tem mais
      // função nenhuma e não pode sobreviver ao fluxo.
      clearRegistration()
      clearResendCooldown()
      queryClient.setQueryData(sessionQueryOptions.queryKey, session)
      navigate({
        to: '/',
        replace: true,
        state: {
          flash: { tone: 'success', message: 'E-mail confirmado. Sua conta está pronta.' },
        },
      })
    },
    onError() {
      // O campo não é limpo: o usuário precisa ver o que digitou. Selecionado,
      // o próximo dígito já substitui tudo. Quem anuncia é o role="alert".
      codeRef.current?.focus()
      codeRef.current?.select()
    },
  })

  const resend = useMutation({
    mutationFn: resendCode,
    onSuccess(result) {
      // O 202 traz SEMPRE um token bem formado, e ele SUBSTITUI o guardado.
      // Quem manda sobre a qual tentativa o código novo pertence é o servidor —
      // o cliente obedece à resposta em vez de presumir que o anterior segue
      // valendo. O endereço é o mesmo, então o par continua coerente.
      setToken(saveRegistration(result.registrationToken, knownEmail))
      startNextCooldown()
      setResendDone(true)
      form.setValue('code', '')
      codeRef.current?.focus()
    },
  })

  function handleResend() {
    // `resend-code` exige o token deste endereço. O botão nem chega a ser
    // renderizado sem ele — esta guarda é para o tipo.
    if (token === null) return
    setResendDone(false)
    resend.mutate({ email: knownEmail, registrationToken: token })
  }

  const onSubmit = form.handleSubmit((values) => {
    if (verify.isPending) return
    // Sem token não existe tentativa a validar: o backend recusaria com 400 e a
    // tela já mostra o caminho de recuperação em vez do formulário.
    if (token === null) return
    verify.mutate({ ...values, email: knownEmail, registrationToken: token })
  })

  const resendError = resend.error
    ? resend.error instanceof ApiError && resend.error.status === 429
      ? 'Você pediu códigos demais. Aguarde alguns minutos antes de tentar de novo.'
      : messageForError(resend.error)
    : undefined

  // Sem o token deste endereço, `resend-code` não é a saída: ele exige o token
  // e, sem ele, devolve o mesmo 202 sem emitir nada. A recuperação garantida é
  // refazer o cadastro, que sempre funciona e sempre devolve um token novo.
  let notice: ReactNode
  if (!hasToken) {
    notice = (
      <Alert
        tone="info"
        title="Refaça o cadastro para receber um código novo"
        action={
          <TextLink to="/criar-conta" {...(knownEmail ? { state: { email: knownEmail } } : {})}>
            Ir para o cadastro
          </TextLink>
        }
      >
        É assim que um código enviado para o seu e-mail não serve na mão de mais ninguém. Se você
        abriu esta tela em outra aba, em outro navegador ou em outro dispositivo, preencha o
        cadastro de novo com o mesmo e-mail.
      </Alert>
    )
  } else if (flash) {
    notice = <FlashAlert flash={flash} />
  }

  return (
    <AuthLayout
      documentTitle="Confirme seu e-mail"
      eyebrow="Nova conta"
      step="2 de 2"
      title="Confirme seu e-mail"
      intro={introFor(hasToken, knownEmail)}
      notice={notice}
      footer={
        <>
          <p>
            Errou o e-mail? <TextLink to="/criar-conta">Começar de novo</TextLink>
          </p>
          <p>
            Já tem conta? <TextLink to="/entrar">Entrar</TextLink>
          </p>
        </>
      }
    >
      {hasToken ? (
        <AuthForm
          onSubmit={onSubmit}
          error={verify.error ? INVALID_CODE : undefined}
          focusError={false}
          primary={
            <Button type="submit" variant="primary" fullWidth loading={verify.isPending}>
              Confirmar e-mail
            </Button>
          }
          secondary={
            <ResendCode secondsLeft={secondsLeft} busy={resend.isPending} onResend={handleResend} />
          }
          note={
            <>
              <p role="status">
                {resendDone ? 'Se o e-mail estiver cadastrado, enviamos um novo código.' : ''}
              </p>
              {resendError ? <Alert tone="error">{resendError}</Alert> : null}
            </>
          }
        >
          <Controller
            control={form.control}
            name="code"
            render={({ field }) => (
              <CodeInput
                ref={codeRef}
                value={field.value}
                onChange={(value) => {
                  if (verify.isError) verify.reset()
                  field.onChange(value)
                }}
                onComplete={() => {
                  void onSubmit()
                }}
                busy={verify.isPending}
                autoFocus
                invalid={verify.isError}
                error={form.formState.errors.code?.message}
              />
            )}
          />
        </AuthForm>
      ) : null}
    </AuthLayout>
  )
}

/** Duas aberturas: com o token deste endereço, e sem ele — quando o que a pessoa
 *  precisa saber é por que o código não vale aqui. Nenhuma das duas revela se o
 *  endereço existe. */
function introFor(hasToken: boolean, email: string): ReactNode {
  if (!hasToken) return 'O código de 6 dígitos só vale no navegador em que o cadastro foi pedido.'
  return (
    <>
      Enviamos um código de 6 dígitos para <strong>{email}</strong>. Ele vale por 15 minutos.
    </>
  )
}
