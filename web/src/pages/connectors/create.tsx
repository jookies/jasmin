import { Create, useForm } from "@refinedev/antd";
import { ConnectorFields } from "./form";

export const ConnectorCreate = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Create saveButtonProps={saveButtonProps}>
      <ConnectorFields formProps={formProps} />
    </Create>
  );
};
